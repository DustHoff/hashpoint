-- 0009: Optional external-order mapping per tag.
-- The Tag-Manager combobox writes either an order name supplied by a
-- tag_provider plugin (live-pulled, never cached) or the user's freitext
-- string. NULL ⇒ no mapping; the column has no semantic effect beyond
-- being shown in the editor.

ALTER TABLE tags ADD COLUMN order_name TEXT;
