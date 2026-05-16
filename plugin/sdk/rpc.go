package sdk

import (
	"context"
	"errors"
	"fmt"
	"net/rpc"
	"time"

	hplugin "github.com/hashicorp/go-plugin"
)

// This file wires the SDK interfaces (Plugin, OnCallDocumentationHandler,
// HostAPI) onto hashicorp/go-plugin's net/rpc transport.
//
// Three connections matter:
//
//  1. host → plugin: Plugin methods (Init, Metadata, Configure). The plugin
//     registers a coreServer on the "plugin" plugin-set key; the host
//     dispenses a coreClient and calls "Plugin.X" against it.
//
//  2. host → plugin: capability methods (e.g. OnCallDocumentationHandler.
//     Submit). Same pattern, different plugin-set key. Each capability
//     gets its own broker stream so a long-running Submit cannot block
//     unrelated calls.
//
//  3. plugin → host: HostAPI methods (RedeemSecret, Log). Reverse RPC over
//     the broker: at Init time the host allocates a stream ID, starts a
//     net/rpc server on it serving hostAPIServer, and ships the stream ID
//     to the plugin in CoreInitArgs. The plugin dials the stream and
//     wraps it as a hostAPIClient.
//
// All RPC reply structs carry an explicit Err string (and, where relevant,
// type-discriminator flags) so well-known sentinel errors can be
// reconstructed on the caller's side — net/rpc's native error-return
// flattens to a bare string and loses errors.Is identity.

// ---------------------------------------------------------------------
// Core plugin adapter (Plugin interface).
// ---------------------------------------------------------------------

type corePluginAdapter struct {
	// impl is set only on the plugin (server) side. Nil on the host side.
	impl Plugin
}

// Server is invoked by hashicorp/go-plugin in the plugin process.
func (p *corePluginAdapter) Server(broker *hplugin.MuxBroker) (interface{}, error) {
	return &coreServer{impl: p.impl, broker: broker}, nil
}

// Client is invoked by hashicorp/go-plugin in the host process.
func (p *corePluginAdapter) Client(broker *hplugin.MuxBroker, c *rpc.Client) (interface{}, error) {
	return &coreClient{client: c, broker: broker}, nil
}

type coreServer struct {
	impl   Plugin
	broker *hplugin.MuxBroker
}

// CoreInitArgs carries the HostAPI broker stream-ID the plugin dials back to.
type CoreInitArgs struct {
	HostAPIBrokerID uint32
}

// CoreInitReply mirrors Plugin.Init's error.
type CoreInitReply struct {
	Err string
}

// Init is the net/rpc-callable Plugin.Init. Returns the impl's error via
// the reply struct rather than the RPC error channel so the host can still
// inspect specific sentinel types if we add them later.
func (s *coreServer) Init(args CoreInitArgs, reply *CoreInitReply) error {
	conn, err := s.broker.Dial(args.HostAPIBrokerID)
	if err != nil {
		reply.Err = fmt.Sprintf("dial host api: %v", err)
		return nil
	}
	host := &hostAPIClient{client: rpc.NewClient(conn)}
	if err := s.impl.Init(context.Background(), host); err != nil {
		reply.Err = err.Error()
	}
	return nil
}

// CoreMetadataArgs is the empty arg type for Plugin.Metadata.
type CoreMetadataArgs struct{}

// CoreMetadataReply carries Metadata or an error string.
type CoreMetadataReply struct {
	Metadata Metadata
	Err      string
}

// Metadata is the net/rpc-callable Plugin.Metadata.
func (s *coreServer) Metadata(_ CoreMetadataArgs, reply *CoreMetadataReply) error {
	m, err := s.impl.Metadata(context.Background())
	if err != nil {
		reply.Err = err.Error()
		return nil
	}
	reply.Metadata = m
	return nil
}

// CoreConfigureArgs ships the plugin config.
type CoreConfigureArgs struct {
	Config PluginConfig
}

// CoreConfigureReply mirrors Plugin.Configure's error. IsConfig is true
// when the wrapped error is ErrConfigInvalid — the host renders a
// dedicated "configure me" banner in that case.
type CoreConfigureReply struct {
	Err      string
	IsConfig bool
}

// Configure is the net/rpc-callable Plugin.Configure.
func (s *coreServer) Configure(args CoreConfigureArgs, reply *CoreConfigureReply) error {
	if err := s.impl.Configure(context.Background(), args.Config); err != nil {
		reply.Err = err.Error()
		reply.IsConfig = errors.Is(err, ErrConfigInvalid)
	}
	return nil
}

// coreClient is dispensed to the host as the Plugin interface; it forwards
// each method call to the plugin process over net/rpc.
type coreClient struct {
	client *rpc.Client
	broker *hplugin.MuxBroker
}

// Init opens a reverse-RPC stream, starts hostAPIServer on it, and tells
// the plugin to dial that stream.
func (c *coreClient) Init(_ context.Context, host HostAPI) error {
	streamID := c.broker.NextId()
	go serveHostAPI(c.broker, streamID, host)

	var reply CoreInitReply
	if err := c.client.Call("Plugin.Init", CoreInitArgs{HostAPIBrokerID: streamID}, &reply); err != nil {
		return err
	}
	if reply.Err != "" {
		return errors.New(reply.Err)
	}
	return nil
}

// Metadata returns the plugin's self-description.
func (c *coreClient) Metadata(_ context.Context) (Metadata, error) {
	var reply CoreMetadataReply
	if err := c.client.Call("Plugin.Metadata", CoreMetadataArgs{}, &reply); err != nil {
		return Metadata{}, err
	}
	if reply.Err != "" {
		return Metadata{}, errors.New(reply.Err)
	}
	return reply.Metadata, nil
}

// Configure pushes new settings to the plugin.
func (c *coreClient) Configure(_ context.Context, cfg PluginConfig) error {
	var reply CoreConfigureReply
	if err := c.client.Call("Plugin.Configure", CoreConfigureArgs{Config: cfg}, &reply); err != nil {
		return err
	}
	if reply.Err == "" {
		return nil
	}
	if reply.IsConfig {
		return fmt.Errorf("%w: %s", ErrConfigInvalid, reply.Err)
	}
	return errors.New(reply.Err)
}

// ---------------------------------------------------------------------
// OnCall capability adapter.
// ---------------------------------------------------------------------

type oncallPluginAdapter struct {
	impl OnCallDocumentationHandler // nil on host side
}

// Server implements hashicorp/go-plugin's Plugin interface: it returns
// the RPC-server stub the host registers when a plugin process starts.
func (p *oncallPluginAdapter) Server(_ *hplugin.MuxBroker) (interface{}, error) {
	return &oncallServer{impl: p.impl}, nil
}

// Client implements hashicorp/go-plugin's Plugin interface: it returns
// the RPC-client stub the host wires up to call into the plugin.
func (p *oncallPluginAdapter) Client(_ *hplugin.MuxBroker, c *rpc.Client) (interface{}, error) {
	return &oncallClient{client: c}, nil
}

type oncallServer struct {
	impl OnCallDocumentationHandler
}

// OnCallSubmitArgs carries the document to file.
type OnCallSubmitArgs struct {
	Document OnCallDocument
}

// OnCallSubmitReply carries SubmissionResult or a typed error.
type OnCallSubmitReply struct {
	Result          SubmissionResult
	Err             string
	IsTransient     bool
	IsNotConfigured bool
}

// Submit is the net/rpc-callable OnCallDocumentationHandler.Submit.
func (s *oncallServer) Submit(args OnCallSubmitArgs, reply *OnCallSubmitReply) error {
	res, err := s.impl.Submit(context.Background(), args.Document)
	if err != nil {
		reply.Err = err.Error()
		reply.IsTransient = errors.Is(err, ErrTransient)
		reply.IsNotConfigured = errors.Is(err, ErrNotConfigured)
		return nil
	}
	reply.Result = res
	return nil
}

type oncallClient struct {
	client *rpc.Client
}

// Submit forwards the document to the plugin and rehydrates ErrTransient /
// ErrNotConfigured on the host side.
func (c *oncallClient) Submit(_ context.Context, doc OnCallDocument) (SubmissionResult, error) {
	var reply OnCallSubmitReply
	if err := c.client.Call("Plugin.Submit", OnCallSubmitArgs{Document: doc}, &reply); err != nil {
		return SubmissionResult{}, err
	}
	if reply.Err == "" {
		return reply.Result, nil
	}
	switch {
	case reply.IsTransient:
		return SubmissionResult{}, fmt.Errorf("%w: %s", ErrTransient, reply.Err)
	case reply.IsNotConfigured:
		return SubmissionResult{}, fmt.Errorf("%w: %s", ErrNotConfigured, reply.Err)
	default:
		return SubmissionResult{}, errors.New(reply.Err)
	}
}

// ---------------------------------------------------------------------
// Plugin management capability adapter.
// ---------------------------------------------------------------------

type mgmtPluginAdapter struct {
	impl PluginManagementHandler // nil on host side
}

// Server returns the RPC-server stub running inside the plugin process.
func (p *mgmtPluginAdapter) Server(_ *hplugin.MuxBroker) (interface{}, error) {
	return &mgmtServer{impl: p.impl}, nil
}

// Client returns the RPC-client stub the host wires up to call into the plugin.
func (p *mgmtPluginAdapter) Client(_ *hplugin.MuxBroker, c *rpc.Client) (interface{}, error) {
	return &mgmtClient{client: c}, nil
}

type mgmtServer struct {
	impl PluginManagementHandler
}

// MgmtListAvailableArgs is the empty arg type for ListAvailable.
type MgmtListAvailableArgs struct{}

// MgmtListAvailableReply carries the catalog or an error string.
type MgmtListAvailableReply struct {
	Available []AvailablePlugin
	Err       string
}

// ListAvailable is the net/rpc-callable PluginManagementHandler.ListAvailable.
func (s *mgmtServer) ListAvailable(_ MgmtListAvailableArgs, reply *MgmtListAvailableReply) error {
	out, err := s.impl.ListAvailable(context.Background())
	if err != nil {
		reply.Err = err.Error()
		return nil
	}
	reply.Available = out
	return nil
}

// MgmtNameArgs is the shared arg type for Install/Update/Uninstall — all
// three are keyed off the AvailablePlugin.Name. Keeping a single struct
// here means adding e.g. an "force" flag later is a single edit.
type MgmtNameArgs struct {
	Name string
}

// MgmtErrReply carries only an error string. Used by Install/Update/Uninstall.
type MgmtErrReply struct {
	Err string
}

// Install is the net/rpc-callable PluginManagementHandler.Install.
func (s *mgmtServer) Install(args MgmtNameArgs, reply *MgmtErrReply) error {
	if err := s.impl.Install(context.Background(), args.Name); err != nil {
		reply.Err = err.Error()
	}
	return nil
}

// Update is the net/rpc-callable PluginManagementHandler.Update.
func (s *mgmtServer) Update(args MgmtNameArgs, reply *MgmtErrReply) error {
	if err := s.impl.Update(context.Background(), args.Name); err != nil {
		reply.Err = err.Error()
	}
	return nil
}

// Uninstall is the net/rpc-callable PluginManagementHandler.Uninstall.
func (s *mgmtServer) Uninstall(args MgmtNameArgs, reply *MgmtErrReply) error {
	if err := s.impl.Uninstall(context.Background(), args.Name); err != nil {
		reply.Err = err.Error()
	}
	return nil
}

type mgmtClient struct {
	client *rpc.Client
}

// ListAvailable forwards to the plugin and returns the merged catalog
// the host then decorates with InstalledVersion / SourcePlugin.
func (c *mgmtClient) ListAvailable(_ context.Context) ([]AvailablePlugin, error) {
	var reply MgmtListAvailableReply
	if err := c.client.Call("Plugin.ListAvailable", MgmtListAvailableArgs{}, &reply); err != nil {
		return nil, err
	}
	if reply.Err != "" {
		return nil, errors.New(reply.Err)
	}
	return reply.Available, nil
}

// Install asks the plugin to materialise <PluginsDir>/<name>/ on disk.
func (c *mgmtClient) Install(_ context.Context, name string) error {
	return c.callNameOnly("Plugin.Install", name)
}

// Update asks the plugin to refresh <PluginsDir>/<name>/. The host has
// already stopped the target subprocess before calling.
func (c *mgmtClient) Update(_ context.Context, name string) error {
	return c.callNameOnly("Plugin.Update", name)
}

// Uninstall asks the plugin to remove <PluginsDir>/<name>/ from disk.
// The host clears DB rows after this returns.
func (c *mgmtClient) Uninstall(_ context.Context, name string) error {
	return c.callNameOnly("Plugin.Uninstall", name)
}

func (c *mgmtClient) callNameOnly(method, name string) error {
	var reply MgmtErrReply
	if err := c.client.Call(method, MgmtNameArgs{Name: name}, &reply); err != nil {
		return err
	}
	if reply.Err != "" {
		return errors.New(reply.Err)
	}
	return nil
}

// ---------------------------------------------------------------------
// Process auto-tag capability adapter.
// ---------------------------------------------------------------------

type processAutoTagPluginAdapter struct {
	impl ProcessAutoTagHandler // nil on host side
}

// Server returns the RPC-server stub running inside the plugin process.
func (p *processAutoTagPluginAdapter) Server(_ *hplugin.MuxBroker) (interface{}, error) {
	return &processAutoTagServer{impl: p.impl}, nil
}

// Client returns the RPC-client stub the host wires up to call into the plugin.
func (p *processAutoTagPluginAdapter) Client(_ *hplugin.MuxBroker, c *rpc.Client) (interface{}, error) {
	return &processAutoTagClient{client: c}, nil
}

type processAutoTagServer struct {
	impl ProcessAutoTagHandler
}

// ProcessAutoTagNamesArgs is the empty arg type for ProcessNames.
type ProcessAutoTagNamesArgs struct{}

// ProcessAutoTagNamesReply carries the declared basenames or an error string.
type ProcessAutoTagNamesReply struct {
	Names []string
	Err   string
}

// ProcessNames is the net/rpc-callable ProcessAutoTagHandler.ProcessNames.
func (s *processAutoTagServer) ProcessNames(_ ProcessAutoTagNamesArgs, reply *ProcessAutoTagNamesReply) error {
	names, err := s.impl.ProcessNames(context.Background())
	if err != nil {
		reply.Err = err.Error()
		return nil
	}
	reply.Names = names
	return nil
}

// ProcessAutoTagResolveArgs ships the focus event the handler should
// inspect.
type ProcessAutoTagResolveArgs struct {
	Info ProcessFocusInfo
}

// ProcessAutoTagResolveReply carries the handler's verdict or an error
// string. IsNotConfigured is set when the handler returned
// ErrNotConfigured so the host can distinguish "plugin not ready" from
// other failures.
type ProcessAutoTagResolveReply struct {
	Result          ProcessAutoTagResult
	Err             string
	IsNotConfigured bool
}

// Resolve is the net/rpc-callable ProcessAutoTagHandler.Resolve.
func (s *processAutoTagServer) Resolve(args ProcessAutoTagResolveArgs, reply *ProcessAutoTagResolveReply) error {
	res, err := s.impl.Resolve(context.Background(), args.Info)
	if err != nil {
		reply.Err = err.Error()
		reply.IsNotConfigured = errors.Is(err, ErrNotConfigured)
		return nil
	}
	reply.Result = res
	return nil
}

type processAutoTagClient struct {
	client *rpc.Client
}

// ProcessNames forwards to the plugin and rehydrates the response.
func (c *processAutoTagClient) ProcessNames(_ context.Context) ([]string, error) {
	var reply ProcessAutoTagNamesReply
	if err := c.client.Call("Plugin.ProcessNames", ProcessAutoTagNamesArgs{}, &reply); err != nil {
		return nil, err
	}
	if reply.Err != "" {
		return nil, errors.New(reply.Err)
	}
	return reply.Names, nil
}

// Resolve forwards the focus info to the plugin and rehydrates
// ErrNotConfigured on the host side.
func (c *processAutoTagClient) Resolve(_ context.Context, info ProcessFocusInfo) (ProcessAutoTagResult, error) {
	var reply ProcessAutoTagResolveReply
	if err := c.client.Call("Plugin.Resolve", ProcessAutoTagResolveArgs{Info: info}, &reply); err != nil {
		return ProcessAutoTagResult{}, err
	}
	if reply.Err == "" {
		return reply.Result, nil
	}
	if reply.IsNotConfigured {
		return ProcessAutoTagResult{}, fmt.Errorf("%w: %s", ErrNotConfigured, reply.Err)
	}
	return ProcessAutoTagResult{}, errors.New(reply.Err)
}

// ---------------------------------------------------------------------
// Off-hours provider capability adapter.
// ---------------------------------------------------------------------

type offHoursPluginAdapter struct {
	impl OffHoursProviderHandler // nil on host side
}

// Server returns the RPC-server stub running inside the plugin process.
func (p *offHoursPluginAdapter) Server(_ *hplugin.MuxBroker) (interface{}, error) {
	return &offHoursServer{impl: p.impl}, nil
}

// Client returns the RPC-client stub the host wires up to call into the plugin.
func (p *offHoursPluginAdapter) Client(_ *hplugin.MuxBroker, c *rpc.Client) (interface{}, error) {
	return &offHoursClient{client: c}, nil
}

type offHoursServer struct {
	impl OffHoursProviderHandler
}

// OffHoursArgs ships the time window the handler should consider.
type OffHoursArgs struct {
	Request OffHoursRequest
}

// OffHoursReply carries the handler's intervals or a typed error.
// IsNotConfigured rehydrates ErrNotConfigured on the host side so the
// caller can distinguish "plugin not ready" from other failures.
type OffHoursReply struct {
	Intervals       []OffHoursInterval
	Err             string
	IsNotConfigured bool
}

// OffHours is the net/rpc-callable OffHoursProviderHandler.OffHours.
func (s *offHoursServer) OffHours(args OffHoursArgs, reply *OffHoursReply) error {
	out, err := s.impl.OffHours(context.Background(), args.Request)
	if err != nil {
		reply.Err = err.Error()
		reply.IsNotConfigured = errors.Is(err, ErrNotConfigured)
		return nil
	}
	reply.Intervals = out
	return nil
}

type offHoursClient struct {
	client *rpc.Client
}

// OffHours forwards the request to the plugin and rehydrates
// ErrNotConfigured on the host side.
func (c *offHoursClient) OffHours(_ context.Context, req OffHoursRequest) ([]OffHoursInterval, error) {
	var reply OffHoursReply
	if err := c.client.Call("Plugin.OffHours", OffHoursArgs{Request: req}, &reply); err != nil {
		return nil, err
	}
	if reply.Err == "" {
		return reply.Intervals, nil
	}
	if reply.IsNotConfigured {
		return nil, fmt.Errorf("%w: %s", ErrNotConfigured, reply.Err)
	}
	return nil, errors.New(reply.Err)
}

// ---------------------------------------------------------------------
// Tag provider capability adapter.
// ---------------------------------------------------------------------

type tagProviderPluginAdapter struct {
	impl TagProviderHandler // nil on host side
}

// Server returns the RPC-server stub running inside the plugin process.
func (p *tagProviderPluginAdapter) Server(_ *hplugin.MuxBroker) (interface{}, error) {
	return &tagProviderServer{impl: p.impl}, nil
}

// Client returns the RPC-client stub the host wires up to call into the plugin.
func (p *tagProviderPluginAdapter) Client(_ *hplugin.MuxBroker, c *rpc.Client) (interface{}, error) {
	return &tagProviderClient{client: c}, nil
}

type tagProviderServer struct {
	impl TagProviderHandler
}

// TagProviderListArgs is the empty arg type for ListTags.
type TagProviderListArgs struct{}

// TagProviderListReply carries the plugin's imported tags or an error.
type TagProviderListReply struct {
	Tags []ImportedTag
	Err  string
}

// ListTags is the net/rpc-callable TagProviderHandler.ListTags.
func (s *tagProviderServer) ListTags(_ TagProviderListArgs, reply *TagProviderListReply) error {
	out, err := s.impl.ListTags(context.Background())
	if err != nil {
		reply.Err = err.Error()
		return nil
	}
	reply.Tags = out
	return nil
}

// TagProviderListOrdersArgs is the empty arg type for ListOrders.
type TagProviderListOrdersArgs struct{}

// TagProviderListOrdersReply carries the plugin's order catalogue or
// an error. The host populates the Tags-tab Auftrag dropdown from it.
type TagProviderListOrdersReply struct {
	Orders []Order
	Err    string
}

// ListOrders is the net/rpc-callable TagProviderHandler.ListOrders.
func (s *tagProviderServer) ListOrders(_ TagProviderListOrdersArgs, reply *TagProviderListOrdersReply) error {
	out, err := s.impl.ListOrders(context.Background())
	if err != nil {
		reply.Err = err.Error()
		return nil
	}
	reply.Orders = out
	return nil
}

// TagProviderNotifyOrdersArgs ships the full tag-order mapping
// snapshot from the host to the plugin on every user-initiated tag
// mutation.
type TagProviderNotifyOrdersArgs struct {
	Mappings []TagOrderMapping
}

// TagProviderNotifyOrdersReply carries an optional error string. The
// host treats the call fire-and-forget — a non-empty Err is logged at
// Debug on the host side and never surfaced to the user.
type TagProviderNotifyOrdersReply struct {
	Err string
}

// NotifyTagOrders is the net/rpc-callable TagProviderHandler.NotifyTagOrders.
func (s *tagProviderServer) NotifyTagOrders(args TagProviderNotifyOrdersArgs, reply *TagProviderNotifyOrdersReply) error {
	if err := s.impl.NotifyTagOrders(context.Background(), args.Mappings); err != nil {
		reply.Err = err.Error()
	}
	return nil
}

type tagProviderClient struct {
	client *rpc.Client
}

// ListTags forwards to the plugin and returns its catalogue.
func (c *tagProviderClient) ListTags(_ context.Context) ([]ImportedTag, error) {
	var reply TagProviderListReply
	if err := c.client.Call("Plugin.ListTags", TagProviderListArgs{}, &reply); err != nil {
		return nil, err
	}
	if reply.Err != "" {
		return nil, errors.New(reply.Err)
	}
	return reply.Tags, nil
}

// ListOrders forwards to the plugin and returns its order catalogue.
func (c *tagProviderClient) ListOrders(_ context.Context) ([]Order, error) {
	var reply TagProviderListOrdersReply
	if err := c.client.Call("Plugin.ListOrders", TagProviderListOrdersArgs{}, &reply); err != nil {
		return nil, err
	}
	if reply.Err != "" {
		return nil, errors.New(reply.Err)
	}
	return reply.Orders, nil
}

// NotifyTagOrders forwards the snapshot to the plugin. An RPC-transport
// error surfaces here so the host's fan-out can log it; the plugin's
// own error (rehydrated from reply.Err) gets the same treatment.
func (c *tagProviderClient) NotifyTagOrders(_ context.Context, mappings []TagOrderMapping) error {
	var reply TagProviderNotifyOrdersReply
	if err := c.client.Call("Plugin.NotifyTagOrders", TagProviderNotifyOrdersArgs{Mappings: mappings}, &reply); err != nil {
		return err
	}
	if reply.Err != "" {
		return errors.New(reply.Err)
	}
	return nil
}

// ---------------------------------------------------------------------
// HostAPI (reverse RPC: plugin → host).
// ---------------------------------------------------------------------

// serveHostAPI registers a hostAPIServer on the given broker stream and
// blocks serving connections. Called once per plugin from coreClient.Init;
// the goroutine exits when the broker drops the stream (typically on
// plugin shutdown).
func serveHostAPI(broker *hplugin.MuxBroker, streamID uint32, host HostAPI) {
	conn, err := broker.Accept(streamID)
	if err != nil {
		return
	}
	srv := rpc.NewServer()
	// RegisterName tags every exported method as "HostAPI.MethodName".
	// Mismatch with the client side ("HostAPI.RedeemSecret" etc.) would
	// surface as "rpc: can't find method" — catch in tests.
	if err := srv.RegisterName("HostAPI", &hostAPIServer{impl: host}); err != nil {
		_ = conn.Close()
		return
	}
	srv.ServeConn(conn)
}

type hostAPIServer struct {
	impl HostAPI
}

// HostRedeemSecretArgs identifies the handle to redeem.
type HostRedeemSecretArgs struct {
	Handle SecretHandle
}

// HostRedeemSecretReply carries plaintext or a typed error.
type HostRedeemSecretReply struct {
	Value     string
	Err       string
	IsUnknown bool
}

// RedeemSecret is the net/rpc-callable HostAPI.RedeemSecret.
func (s *hostAPIServer) RedeemSecret(args HostRedeemSecretArgs, reply *HostRedeemSecretReply) error {
	v, err := s.impl.RedeemSecret(context.Background(), args.Handle)
	if err != nil {
		reply.Err = err.Error()
		reply.IsUnknown = errors.Is(err, ErrUnknownSecretHandle)
		return nil
	}
	reply.Value = v
	return nil
}

// HostLogArgs is the structured-log payload from the plugin.
type HostLogArgs struct {
	Level   string
	Message string
	Fields  map[string]string
}

// HostLogReply mirrors Log's error.
type HostLogReply struct {
	Err string
}

// Log is the net/rpc-callable HostAPI.Log.
func (s *hostAPIServer) Log(args HostLogArgs, reply *HostLogReply) error {
	if err := s.impl.Log(context.Background(), args.Level, args.Message, args.Fields); err != nil {
		reply.Err = err.Error()
	}
	return nil
}

// HostRequestEntraTokenArgs carries the scopes the plugin wants a token
// for. Scopes are passed straight through to MSAL on the host side.
type HostRequestEntraTokenArgs struct {
	Scopes []string
}

// HostRequestEntraTokenReply carries the access token and its UTC
// expiry, or a typed error. IsNotAvailable rehydrates
// ErrEntraNotAvailable on the plugin side.
type HostRequestEntraTokenReply struct {
	Token          string
	ExpiresAt      time.Time
	Err            string
	IsNotAvailable bool
}

// RequestEntraToken is the net/rpc-callable HostAPI.RequestEntraToken.
func (s *hostAPIServer) RequestEntraToken(args HostRequestEntraTokenArgs, reply *HostRequestEntraTokenReply) error {
	token, expiresAt, err := s.impl.RequestEntraToken(context.Background(), args.Scopes)
	if err != nil {
		reply.Err = err.Error()
		reply.IsNotAvailable = errors.Is(err, ErrEntraNotAvailable)
		return nil
	}
	reply.Token = token
	reply.ExpiresAt = expiresAt
	return nil
}

// HostRequestPersonioSessionArgs is the empty arg type for
// HostAPI.RequestPersonioSession.
type HostRequestPersonioSessionArgs struct{}

// HostRequestPersonioSessionReply carries the captured session or a
// typed error. IsNotAvailable rehydrates ErrPersonioNotAvailable on the
// plugin side so callers can errors.Is against the sentinel.
type HostRequestPersonioSessionReply struct {
	Session        PersonioSession
	Err            string
	IsNotAvailable bool
}

// RequestPersonioSession is the net/rpc-callable HostAPI.RequestPersonioSession.
func (s *hostAPIServer) RequestPersonioSession(_ HostRequestPersonioSessionArgs, reply *HostRequestPersonioSessionReply) error {
	sess, err := s.impl.RequestPersonioSession(context.Background())
	if err != nil {
		reply.Err = err.Error()
		reply.IsNotAvailable = errors.Is(err, ErrPersonioNotAvailable)
		return nil
	}
	reply.Session = sess
	return nil
}

// HostListTagsArgs is the empty arg type for HostAPI.ListTags.
type HostListTagsArgs struct{}

// HostListTagsReply carries the host's projected tag set or an error.
type HostListTagsReply struct {
	Tags []HostTag
	Err  string
}

// ListTags is the net/rpc-callable HostAPI.ListTags.
func (s *hostAPIServer) ListTags(_ HostListTagsArgs, reply *HostListTagsReply) error {
	out, err := s.impl.ListTags(context.Background())
	if err != nil {
		reply.Err = err.Error()
		return nil
	}
	reply.Tags = out
	return nil
}

// HostPublishTagsArgs carries the imports a tag_provider plugin wants
// merged into the host store.
type HostPublishTagsArgs struct {
	Tags []ImportedTag
}

// HostPublishTagsReply carries the count of actually-created tags or
// a typed error. IsNotAllowed rehydrates ErrPublishTagsNotAllowed on
// the plugin side so callers can errors.Is against the sentinel.
type HostPublishTagsReply struct {
	Created      int
	Err          string
	IsNotAllowed bool
}

// PublishTags is the net/rpc-callable HostAPI.PublishTags.
func (s *hostAPIServer) PublishTags(args HostPublishTagsArgs, reply *HostPublishTagsReply) error {
	n, err := s.impl.PublishTags(context.Background(), args.Tags)
	if err != nil {
		reply.Err = err.Error()
		reply.IsNotAllowed = errors.Is(err, ErrPublishTagsNotAllowed)
		return nil
	}
	reply.Created = n
	return nil
}

// hostAPIClient is what the plugin sees as sdk.HostAPI.
type hostAPIClient struct {
	client *rpc.Client
}

// RedeemSecret asks the host for the plaintext.
func (c *hostAPIClient) RedeemSecret(_ context.Context, h SecretHandle) (string, error) {
	var reply HostRedeemSecretReply
	if err := c.client.Call("HostAPI.RedeemSecret", HostRedeemSecretArgs{Handle: h}, &reply); err != nil {
		return "", err
	}
	if reply.IsUnknown {
		return "", fmt.Errorf("%w: %s", ErrUnknownSecretHandle, reply.Err)
	}
	if reply.Err != "" {
		return "", errors.New(reply.Err)
	}
	return reply.Value, nil
}

// Log forwards a structured log line to the host's slog handler.
func (c *hostAPIClient) Log(_ context.Context, level, message string, fields map[string]string) error {
	var reply HostLogReply
	if err := c.client.Call("HostAPI.Log", HostLogArgs{Level: level, Message: message, Fields: fields}, &reply); err != nil {
		return err
	}
	if reply.Err != "" {
		return errors.New(reply.Err)
	}
	return nil
}

// RequestEntraToken asks the host for a Bearer-suitable Entra ID
// access token for the given scopes, plus its UTC expiry.
// ErrEntraNotAvailable is rehydrated on the plugin side so callers can
// `errors.Is` against it.
func (c *hostAPIClient) RequestEntraToken(_ context.Context, scopes []string) (string, time.Time, error) {
	var reply HostRequestEntraTokenReply
	if err := c.client.Call("HostAPI.RequestEntraToken", HostRequestEntraTokenArgs{Scopes: scopes}, &reply); err != nil {
		return "", time.Time{}, err
	}
	if reply.Err == "" {
		return reply.Token, reply.ExpiresAt, nil
	}
	if reply.IsNotAvailable {
		return "", time.Time{}, fmt.Errorf("%w: %s", ErrEntraNotAvailable, reply.Err)
	}
	return "", time.Time{}, errors.New(reply.Err)
}

// RequestPersonioSession asks the host for the current Personio session
// (AppHost, CSRF token, cookies). ErrPersonioNotAvailable is rehydrated
// on the plugin side so callers can `errors.Is` against it.
func (c *hostAPIClient) RequestPersonioSession(_ context.Context) (PersonioSession, error) {
	var reply HostRequestPersonioSessionReply
	if err := c.client.Call("HostAPI.RequestPersonioSession", HostRequestPersonioSessionArgs{}, &reply); err != nil {
		return PersonioSession{}, err
	}
	if reply.Err == "" {
		return reply.Session, nil
	}
	if reply.IsNotAvailable {
		return PersonioSession{}, fmt.Errorf("%w: %s", ErrPersonioNotAvailable, reply.Err)
	}
	return PersonioSession{}, errors.New(reply.Err)
}

// ListTags returns the host's current tag set projected to HostTag.
func (c *hostAPIClient) ListTags(_ context.Context) ([]HostTag, error) {
	var reply HostListTagsReply
	if err := c.client.Call("HostAPI.ListTags", HostListTagsArgs{}, &reply); err != nil {
		return nil, err
	}
	if reply.Err != "" {
		return nil, errors.New(reply.Err)
	}
	return reply.Tags, nil
}

// PublishTags merges the supplied tags into the host store.
// ErrPublishTagsNotAllowed is rehydrated on the plugin side so callers
// can `errors.Is` against it.
func (c *hostAPIClient) PublishTags(_ context.Context, tags []ImportedTag) (int, error) {
	var reply HostPublishTagsReply
	if err := c.client.Call("HostAPI.PublishTags", HostPublishTagsArgs{Tags: tags}, &reply); err != nil {
		return 0, err
	}
	if reply.Err == "" {
		return reply.Created, nil
	}
	if reply.IsNotAllowed {
		return 0, fmt.Errorf("%w: %s", ErrPublishTagsNotAllowed, reply.Err)
	}
	return 0, errors.New(reply.Err)
}
