import { defineConfig } from "@lightning-js/lightning";

export default defineConfig({
  test: {
    include: ["src/**/*.test.{ts,tsx}"],
    exclude: ["dist/**", "node_modules/**"],
    environment: "node",
    pool: "inline",
    isolate: true,
    reporters: ["default"],
  },
});
