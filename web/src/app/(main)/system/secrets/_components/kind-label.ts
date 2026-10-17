import type { SecretKind } from "@/lib/types";

// 类型标签的单一维护点：列表、详情、新建表单共用。
// 后端校验的类型集合见 secret/secret.go；这里新增类型时要同步。
export const KIND_LABEL: Record<SecretKind, string> = {
  api_key: "API Key",
  password: "密码",
  token: "Token",
  cookie: "Cookie",
  certificate: "证书",
  private_key: "私钥",
  connection_string: "连接串",
  other: "其他",
};

export const KIND_OPTIONS = Object.keys(KIND_LABEL) as SecretKind[];
