import type { NextConfig } from "next";
import createNextIntlPlugin from "next-intl/plugin";

const withNextIntl = createNextIntlPlugin("./src/i18n/request.ts");

const nextConfig: NextConfig = {
  // Standalone output (a self-contained server bundle + pruned
  // node_modules) is what Dockerfile's final stage copies - without this,
  // the container image would need the full node_modules tree instead of
  // just the traced production subset. See docs/OPEN_POINTS.md item 34.
  output: "standalone",
};

export default withNextIntl(nextConfig);
