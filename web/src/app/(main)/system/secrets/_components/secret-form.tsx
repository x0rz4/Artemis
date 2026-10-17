"use client";

import * as React from "react";

import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import type { SecretKind } from "@/lib/types";

import { KIND_LABEL, KIND_OPTIONS } from "./kind-label";

export type SecretFormState = {
  name: string;
  kind: SecretKind;
  value: string;
  host: string;
  source: string;
  notes: string;
};

const EMPTY: SecretFormState = {
  name: "",
  kind: "api_key",
  value: "",
  host: "",
  source: "",
  notes: "",
};

// 新建表单。手工录入时值只提交一次；编辑场景不要复用本组件的 value 字段，
// 否则会把掩码值当新值写回（服务端虽会拒绝，但体验上应避免）。
export function SecretForm({
  kinds,
  onCancel,
  onSubmit,
}: {
  kinds?: SecretKind[];
  onCancel: () => void;
  onSubmit: (form: SecretFormState) => Promise<void>;
}) {
  const [form, setForm] = React.useState<SecretFormState>(EMPTY);
  const [saving, setSaving] = React.useState(false);
  const options = kinds?.length ? kinds : KIND_OPTIONS;

  const set = <K extends keyof SecretFormState>(k: K, v: SecretFormState[K]) => setForm((f) => ({ ...f, [k]: v }));

  const submit = async () => {
    if (!form.name.trim()) {
      toast.error("名称必填");
      return;
    }
    if (!form.value.trim()) {
      toast.error("凭据值必填");
      return;
    }
    setSaving(true);
    try {
      await onSubmit({ ...form, name: form.name.trim() });
      toast.success("已保存");
    } catch (err) {
      toast.error("保存失败", { description: (err as Error).message });
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="flex flex-col gap-4 px-4 pb-6">
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="secret-name">名称</Label>
        <Input
          id="secret-name"
          placeholder="如：example.com 后台管理员"
          value={form.name}
          onChange={(e) => set("name", e.target.value)}
        />
      </div>
      <div className="flex flex-col gap-1.5">
        <Label>类型</Label>
        <Select value={form.kind} onValueChange={(v) => set("kind", v as SecretKind)}>
          <SelectTrigger>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {options.map((k) => (
              <SelectItem key={k} value={k}>
                {KIND_LABEL[k]}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="secret-value">凭据值</Label>
        <Textarea
          id="secret-value"
          className="font-mono"
          rows={3}
          placeholder="只提交一次，保存后不会回显"
          value={form.value}
          onChange={(e) => set("value", e.target.value)}
        />
      </div>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="secret-host">host（可选）</Label>
        <Input
          id="secret-host"
          placeholder="example.com"
          value={form.host}
          onChange={(e) => set("host", e.target.value)}
        />
      </div>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="secret-source">来源（可选）</Label>
        <Input
          id="secret-source"
          placeholder="如：目录扫描发现的 .env"
          value={form.source}
          onChange={(e) => set("source", e.target.value)}
        />
      </div>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="secret-notes">说明（可选）</Label>
        <Textarea
          id="secret-notes"
          rows={3}
          placeholder="适用范围、获取方式等；不要重复写明文"
          value={form.notes}
          onChange={(e) => set("notes", e.target.value)}
        />
      </div>
      <div className="flex justify-end gap-2">
        <Button variant="outline" onClick={onCancel} disabled={saving}>
          取消
        </Button>
        <Button onClick={() => void submit()} disabled={saving}>
          {saving ? "保存中…" : "保存"}
        </Button>
      </div>
    </div>
  );
}
