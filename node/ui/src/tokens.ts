/** 设计系统 token（架构分册 §3.4 设计语言：中性色板 + 单一品牌强调色、4/8 间距刻度） */
export const tokens = {
  colors: {
    brand: {
      50: "#eef2ff",
      100: "#e0e7ff",
      500: "#6366f1",
      600: "#4f46e5",
      700: "#4338ca",
    },
  },
  spacing: [0, 4, 8, 12, 16, 24, 32, 48, 64],
  radius: { sm: 6, md: 8, lg: 12 },
  shadow: {
    sm: "0 1px 2px rgba(0,0,0,.05)",
    md: "0 4px 6px -1px rgba(0,0,0,.07), 0 2px 4px -2px rgba(0,0,0,.05)",
  },
  fontSize: { sm: 12, base: 14, lg: 16, xl: 20, "2xl": 24 },
};
