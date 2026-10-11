import packageJson from "../../package.json";

const currentYear = new Date().getFullYear();

export const APP_CONFIG = {
  name: "Artemis",
  version: packageJson.version,
  copyright: `© ${currentYear}, Artemis.`,
  meta: {
    title: "Artemis — 个人自主渗透助手",
    description: "LLM 驱动的个人自主渗透测试系统",
  },
};
