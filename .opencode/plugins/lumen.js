import path from "path";
import { fileURLToPath } from "url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const pluginRoot = path.resolve(__dirname, "../..");
const runCommand = path.join(
  pluginRoot,
  "scripts",
  process.platform === "win32" ? "run.cmd" : "run.sh",
);

export const LumenPlugin = async () => {
  return {
    config: async (config) => {
      config.mcp = config.mcp || {};
      if (!config.mcp.lumen) {
        config.mcp.lumen = {
          type: "local",
          command:
            process.platform === "win32"
              ? ["cmd", "/c", runCommand, "stdio"]
              : ["sh", runCommand, "stdio"],
          enabled: true,
        };
      }
    },
  };
};

export default LumenPlugin;
