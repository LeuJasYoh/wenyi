/** 设计 token 单一真源：颜色/间距/圆角/阴影统一出自设计系统 */
export default {
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  darkMode: "class",
  theme: {
    extend: {
      colors: {
        brand: {
          50: "#eef2ff", 100: "#e0e7ff", 500: "#6366f1", 600: "#4f46e5", 700: "#4338ca",
        },
      },
      borderRadius: { DEFAULT: "8px" },
      boxShadow: {
        card: "0 1px 2px rgba(0,0,0,.05)",
        raised: "0 4px 6px -1px rgba(0,0,0,.07), 0 2px 4px -2px rgba(0,0,0,.05)",
      },
    },
  },
  plugins: [],
};
