/** @type {import('tailwindcss').Config} */
export default {
  content: ["./index.html", "./src/**/*.{js,jsx}"],
  theme: {
    extend: {
      colors: {
        delta: {
          bg: "var(--bg)",
          text: "var(--fg)",
          accent: "var(--acc)",
          dim: "var(--dim)",
          line: "var(--line)",
          card: "var(--card)",
          empty: "var(--empty)",
        },
      },
      fontFamily: {
        mono: ["ui-monospace", "SF Mono", "Menlo", "Cascadia Code", "JetBrains Mono", "monospace"],
      },
    },
  },
  plugins: [],
};
