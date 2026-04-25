/** @type {import('tailwindcss').Config} */
export default {
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  theme: {
    extend: {
      colors: {
        bg: "#1b2636",
        surface: "#243349",
        accent: "#4f8cff",
      },
    },
  },
  plugins: [],
};
