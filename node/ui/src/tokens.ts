/** 设计系统 token（架构分册 §3.4 设计语言：书卷纸感——纸色底、墨色字、朱砂强调，4/8 间距刻度）。
 * Tailwind 侧的单一真源见 ../tailwind.config.js，此处为同步文档。 */
export const tokens = {
  colors: {
    paper: { 50: "#fbf9f4", 100: "#f4f0e6", 200: "#eae4d4", 300: "#dbd2bc" },
    ink: {
      100: "#e6e2d6", 200: "#c9c3b2", 300: "#a8a190", 400: "#7d7666",
      500: "#5c564a", 600: "#453f35", 700: "#332f27", 800: "#272420", 900: "#1e1b17", 950: "#15130f",
    },
    seal: { 50: "#fbf1ec", 100: "#f6dfd5", 300: "#e29880", 500: "#bc4a31", 600: "#a53c26", 700: "#8a3220" },
    moss: { 50: "#f1f5ef", 500: "#5c7a5e", 600: "#4c6750" },
  },
  spacing: [0, 4, 8, 12, 16, 24, 32, 48, 64],
  radius: { sm: 4, DEFAULT: 6, md: 8, lg: 12 },
  shadow: {
    card: "0 1px 2px rgba(28,25,18,.04)",
    raised: "0 2px 4px rgba(28,25,18,.05), 0 10px 28px -14px rgba(28,25,18,.14)",
  },
  fontFamily: {
    serif: '书名/标题/大数字：Georgia + 思源宋体/宋体',
    sans: '正文：系统无衬线（PingFang SC / Microsoft YaHei）',
  },
  fontSize: { sm: 12, base: 14, lg: 16, xl: 20, "2xl": 24 },
};
