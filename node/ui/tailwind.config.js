/** 设计 token 单一真源：书卷纸感设计系统。
 * 纸色（paper）做底、墨色（ink）做字与边、朱砂（seal）做唯一强调色；
 * 竹绿（moss）仅用于成功态。标题/书名/大数字用衬线（font-serif）。
 * brand 保留为 seal 的兼容别名。 */
export default {
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  darkMode: "class",
  theme: {
    extend: {
      colors: {
        paper: {
          50: "#fbf9f4",
          100: "#f4f0e6",
          200: "#eae4d4",
          300: "#dbd2bc",
        },
        ink: {
          50: "#f3f1ea",
          100: "#e6e2d6",
          200: "#c9c3b2",
          300: "#a8a190",
          400: "#7d7666",
          500: "#5c564a",
          600: "#453f35",
          700: "#332f27",
          800: "#272420",
          900: "#1e1b17",
          950: "#15130f",
        },
        seal: {
          50: "#fbf1ec",
          100: "#f6dfd5",
          200: "#eebfae",
          300: "#e29880",
          400: "#d4705a",
          500: "#bc4a31",
          600: "#a53c26",
          700: "#8a3220",
          800: "#6f291b",
        },
        moss: {
          50: "#f1f5ef",
          100: "#dfe8db",
          500: "#5c7a5e",
          600: "#4c6750",
          700: "#3f5644",
        },
        brand: {
          50: "#fbf1ec",
          100: "#f6dfd5",
          500: "#bc4a31",
          600: "#a53c26",
          700: "#8a3220",
        },
      },
      fontFamily: {
        serif: ["Georgia", '"Palatino Linotype"', '"Source Han Serif SC"', '"Noto Serif SC"', '"Songti SC"', "STZhongsong", "STSong", "SimSun", "serif"],
        sans: ["-apple-system", "BlinkMacSystemFont", '"Segoe UI"', '"PingFang SC"', '"Hiragino Sans GB"', '"Microsoft YaHei"', '"Noto Sans CJK SC"', "sans-serif"],
      },
      borderRadius: { DEFAULT: "6px" },
      boxShadow: {
        card: "0 1px 2px rgba(28,25,18,.04)",
        raised: "0 2px 4px rgba(28,25,18,.05), 0 10px 28px -14px rgba(28,25,18,.14)",
      },
    },
  },
  plugins: [],
};
