/** @type {import('tailwindcss').Config} */
module.exports = {
  // The whole dashboard is this UI - every page that can render a Tailwind
  // class needs to be listed here or its classes get purged from the build.
  content: ["./src/rapido-ui/**/*.{ts,tsx}", "./src/pages/**/*.{ts,tsx}"],
  theme: {
    extend: {
      // Same list as --rapido-font-sans (src/index.scss). This exists so
      // `font-sans` means the right thing wherever it is used, rather than
      // leaving it to whatever the browser's default sans-serif is.
      fontFamily: {
        sans: [
          "Inter",
          "Vazirmatn",
          "-apple-system",
          "BlinkMacSystemFont",
          "Segoe UI",
          "Roboto",
          "Oxygen",
          "Ubuntu",
          "Cantarell",
          "Fira Sans",
          "Droid Sans",
          "Helvetica Neue",
          "Tahoma",
          "sans-serif",
        ],
      },
      colors: {
        // The status trio (see --rp-good/-warn/-bad in tailwind.css) takes over
        // Tailwind's own green, amber and red scales, so every existing
        // `text-red-400`, `bg-emerald-500/15`, `border-amber-500/40` follows it.
        ...Object.fromEntries(
          [
            ["emerald", "good"],
            ["green", "good"],
            ["amber", "warn"],
            ["yellow", "warn"],
            ["red", "bad"],
            ["rose", "bad"],
            ["orange", "warm"],
          ].map(([name, v]) => [
            name,
            Object.fromEntries(
              [50, 100, 200, 300, 400, 500, 600, 700, 800, 900].map((shade) => [
                shade,
                `rgb(var(--rp-${v}${shade <= 300 ? "-hi" : shade >= 600 ? "-lo" : ""}) / <alpha-value>)`,
              ])
            ),
          ])
        ),
        // Control-room palette. Every colour is a CSS variable (an RGB
        // triple, so opacity modifiers like bg-rapido-accent/15 still work)
        // defined per theme preset in rapido-ui/tailwind.css and switched by
        // the Appearance settings. Cyan is the cockpit accent, violet and
        // teal its supporting hues. Health/status is never conveyed by
        // these colours; that is Tailwind's own emerald/amber/rose, kept
        // separate so "this is clickable" and "this is healthy" never
        // collide into one meaning.
        rapido: {
          inset: "rgb(var(--rp-inset) / <alpha-value>)",
          good: "rgb(var(--rp-good) / <alpha-value>)",
          warn: "rgb(var(--rp-warn) / <alpha-value>)",
          bad: "rgb(var(--rp-bad) / <alpha-value>)",
          bg: "rgb(var(--rp-bg) / <alpha-value>)",
          surface: "rgb(var(--rp-surface) / <alpha-value>)",
          // A third surface tier - modals, hovers, and anything that should
          // read as "lifted" above a card.
          raised: "rgb(var(--rp-raised) / <alpha-value>)",
          border: "rgb(var(--rp-border) / <alpha-value>)",
          accent: "rgb(var(--rp-accent) / <alpha-value>)",
          teal: "rgb(var(--rp-teal) / <alpha-value>)",
          violet: "rgb(var(--rp-violet) / <alpha-value>)",
          text: "rgb(var(--rp-text) / <alpha-value>)",
          muted: "rgb(var(--rp-muted) / <alpha-value>)",
        },
      },
      // Radii follow the Appearance "corner radius" choice.
      borderRadius: {
        xl2: "var(--rp-r-card)",
        lg: "var(--rp-r-ctl)",
        md: "var(--rp-r-sm)",
      },
      boxShadow: {
        // Layered elevation, built from the active theme and depth setting
        // (see --rp-shadow-* in tailwind.css).
        "rp-1": "var(--rp-shadow-1)",
        "rp-2": "var(--rp-shadow-2)",
        "rp-3": "var(--rp-shadow-3)",
        "rp-inset": "var(--rp-shadow-inset)",
        "rp-glow": "0 0 18px -2px rgb(var(--rp-accent) / .45)",
      },
      keyframes: {
        "rp-fade-up": {
          from: { opacity: "0", transform: "translateY(10px)" },
          to: { opacity: "1", transform: "translateY(0)" },
        },
        "rp-fade": { from: { opacity: "0" }, to: { opacity: "1" } },
        "rp-rise": {
          from: { opacity: "0", transform: "perspective(1200px) translateY(28px) rotateX(14deg) scale(.96)" },
          to: { opacity: "1", transform: "perspective(1200px) translateY(0) rotateX(0) scale(1)" },
        },
        "rp-shimmer": {
          from: { transform: "translateX(-100%)" },
          to: { transform: "translateX(100%)" },
        },
        "rp-flow": {
          from: { transform: "translateX(-16px)" },
          to: { transform: "translateX(0)" },
        },
        "rp-drift-a": {
          "0%, 100%": { transform: "translate3d(0,0,0) scale(1)" },
          "50%": { transform: "translate3d(6%,8%,0) scale(1.12)" },
        },
        "rp-drift-b": {
          "0%, 100%": { transform: "translate3d(0,0,0) scale(1.05)" },
          "50%": { transform: "translate3d(-8%,-5%,0) scale(.95)" },
        },
        "rp-glow-pulse": {
          "0%, 100%": { opacity: ".55" },
          "50%": { opacity: "1" },
        },
      },
      animation: {
        "rp-fade-up": "rp-fade-up .45s cubic-bezier(.2,.7,.2,1) backwards",
        "rp-fade": "rp-fade .25s ease-out backwards",
        "rp-rise": "rp-rise .42s cubic-bezier(.2,.8,.2,1) backwards",
        "rp-shimmer": "rp-shimmer 1.6s ease-in-out infinite",
        "rp-flow": "rp-flow .7s linear infinite",
        "rp-drift-a": "rp-drift-a 38s ease-in-out infinite",
        "rp-drift-b": "rp-drift-b 46s ease-in-out infinite",
        "rp-glow-pulse": "rp-glow-pulse 2.2s ease-in-out infinite",
      },
    },
  },
  plugins: [],
};
