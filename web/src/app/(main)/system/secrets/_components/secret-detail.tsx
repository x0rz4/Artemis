"use client";

import * as React from "react";

import { CopyIcon, EyeIcon, EyeOffIcon, LinkIcon, Trash2Icon, UnlinkIcon } from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { api } from "@/lib/api";
import type { Secret, SecretAccess, SecretLink, SecretLinkTarget } from "@/lib/types";

import { KIND_LABEL } from "./kind-label";

const REVEAL_VISIBLE_MS = 60_000;
const LINK_TYPES: SecretLinkTarget[] = ["task", "asset", "finding", "company", "host", "conversation"];

// 详情抽屉：元数据 + 关联 + 访问审计。
// 明文是独立状态，只由 reveal 接口填充并在 60 秒后自动清除；页面从不持久化它。
export function SecretDetail({
  id,
  onChanged,
  onDeleted,
}: {
  id: number;
  onChanged: () => void;
  onDeleted: () => void;
}) {
  const [secret, setSecret] = React.useState<Secret | null>(null);
  const [access, setAccess] = React.useState<SecretAccess[]>([]);
  const [plain, setPlain] = React.useState("");
  const [revealOpen, setRevealOpen] = React.useState(false);
  const [reason, setReason] = React.useState("");
  const [revealing, setRevealing] = React.useState(false);

  const [notes, setNotes] = React.useState("");
  const [host, setHost] = React.useState("");
  const [newValue, setNewValue] = React.useState("");
  const [saving, setSaving] = React.useState(false);

  const [linkType, setLinkType] = React.useState<SecretLinkTarget>("asset");
  const [linkRef, setLinkRef] = React.useState("");

  const load = React.useCallback(async () => {
    try {
      const s = await api.secret(id);
      setSecret(s);
      setNotes(s.notes);
      setHost(s.host);
      const rows = await api.secretAccess(id);
      setAccess(rows);
    } catch (err) {
      toast.error("读取机密失败", { description: (err as Error).message });
    }
  }, [id]);

  React.useEffect(() => {
    void load();
  }, [load]);

  // 明文只在本地状态里短暂存在。切换条目时父组件用 key 重挂载本组件，
  // 因此这里不需要额外的重置 effect。

  React.useEffect(() => {
    if (!plain) return;
    const t = window.setTimeout(() => setPlain(""), REVEAL_VISIBLE_MS);
    return () => window.clearTimeout(t);
  }, [plain]);

  if (!secret) {
    return <p className="px-4 py-6 text-muted-foreground text-sm">加载中…</p>;
  }

  const reveal = async () => {
    if (!reason.trim()) {
      toast.error("请填写取用原因");
      return;
    }
    setRevealing(true);
    try {
      const r = await api.revealSecret(id, reason.trim());
      setPlain(r.value);
      setRevealOpen(false);
      setReason("");
      void load();
    } catch (err) {
      toast.error("取用失败", { description: (err as Error).message });
    } finally {
      setRevealing(false);
    }
  };

  const save = async () => {
    setSaving(true);
    try {
      const payload: Parameters<typeof api.updateSecret>[1] = { notes, host };
      if (newValue.trim()) {
        payload.value = newValue;
        payload.replace_value = true;
      }
      await api.updateSecret(id, payload);
      setNewValue("");
      toast.success("已保存");
      await load();
      onChanged();
    } catch (err) {
      toast.error("保存失败", { description: (err as Error).message });
    } finally {
      setSaving(false);
    }
  };

  const addLink = async () => {
    if (!linkRef.trim()) {
      toast.error("请填写关联目标");
      return;
    }
    try {
      await api.addSecretLink(id, linkType, linkRef.trim());
      setLinkRef("");
      await load();
      onChanged();
    } catch (err) {
      toast.error("关联失败", { description: (err as Error).message });
    }
  };

  return (
    <div className="flex flex-col gap-5 px-4 pb-8">
      <div className="flex flex-wrap items-center gap-2">
        <h2 className="font-semibold text-lg">{secret.name}</h2>
        <Badge variant="secondary">{KIND_LABEL[secret.kind] ?? secret.kind}</Badge>
        {secret.host && <Badge variant="outline">{secret.host}</Badge>}
      </div>

      <div className="flex flex-col gap-2">
        <Label>凭据值</Label>
        <div className="flex flex-wrap items-center gap-2">
          <code className="rounded bg-muted px-2 py-1 font-mono text-sm">{plain || secret.value}</code>
          {plain ? (
            <>
              <Button
                variant="outline"
                size="sm"
                onClick={() => {
                  void navigator.clipboard.writeText(plain);
                  toast.success("已复制，60 秒后自动隐藏");
                }}
              >
                <CopyIcon data-icon="inline-start" />
                复制
              </Button>
              <Button variant="ghost" size="sm" onClick={() => setPlain("")}>
                <EyeOffIcon data-icon="inline-start" />
                隐藏
              </Button>
            </>
          ) : (
            <Button variant="outline" size="sm" onClick={() => setRevealOpen(true)}>
              <EyeIcon data-icon="inline-start" />
              显示明文
            </Button>
          )}
        </div>
        <p className="text-muted-foreground text-xs">显示明文会记录操作者、时间与原因；明文在 60 秒后自动隐藏。</p>
      </div>

      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="来源" value={secret.source || "—"} />
        <Field label="来源任务" value={secret.source_task_id ? `#${secret.source_task_id}` : "—"} />
        <Field label="创建者" value={secret.created_by || "—"} />
        <Field label="创建时间" value={fmtTime(secret.created_at)} />
        <Field label="最后使用" value={fmtTime(secret.last_used_at)} />
        <Field label="关联数" value={String(secret.link_count)} />
      </div>

      <div className="flex flex-col gap-2">
        <Label>关联</Label>
        <div className="flex flex-col gap-1">
          {(secret.links ?? []).map((l) => (
            <LinkRow
              key={l.id}
              link={l}
              onRemove={async () => {
                try {
                  await api.removeSecretLink(id, l.id);
                  await load();
                  onChanged();
                } catch (err) {
                  toast.error("解除关联失败", { description: (err as Error).message });
                }
              }}
            />
          ))}
          {(secret.links ?? []).length === 0 && <p className="text-muted-foreground text-xs">暂无关联</p>}
        </div>
        <div className="flex flex-wrap items-end gap-2">
          <Select value={linkType} onValueChange={(v) => setLinkType(v as SecretLinkTarget)}>
            <SelectTrigger className="w-36">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {LINK_TYPES.map((t) => (
                <SelectItem key={t} value={t}>
                  {LINK_LABEL[t]}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Input
            className="max-w-48"
            placeholder={linkType === "host" ? "example.com" : "目标 id 或会话 key"}
            value={linkRef}
            onChange={(e) => setLinkRef(e.target.value)}
          />
          <Button variant="outline" onClick={() => void addLink()}>
            <LinkIcon data-icon="inline-start" />
            添加关联
          </Button>
        </div>
      </div>

      <div className="flex flex-col gap-2">
        <Label htmlFor="secret-notes-edit">说明</Label>
        <Textarea id="secret-notes-edit" rows={3} value={notes} onChange={(e) => setNotes(e.target.value)} />
        <Label htmlFor="secret-host-edit">host</Label>
        <Input id="secret-host-edit" value={host} onChange={(e) => setHost(e.target.value)} />
        <Label htmlFor="secret-value-rotate">替换凭据值（留空则不修改）</Label>
        <Input
          id="secret-value-rotate"
          type="password"
          autoComplete="new-password"
          placeholder="提交新值才会替换；掩码值不会被接受"
          value={newValue}
          onChange={(e) => setNewValue(e.target.value)}
        />
        <div className="flex flex-wrap gap-2">
          <Button onClick={() => void save()} disabled={saving}>
            {saving ? "保存中…" : "保存修改"}
          </Button>
          <Button
            variant="destructive"
            onClick={async () => {
              if (!window.confirm(`删除机密「${secret.name}」？关联会被移除，访问审计保留。`)) return;
              try {
                await api.deleteSecret(id);
                toast.success("已删除");
                onDeleted();
              } catch (err) {
                toast.error("删除失败", { description: (err as Error).message });
              }
            }}
          >
            <Trash2Icon data-icon="inline-start" />
            删除
          </Button>
        </div>
      </div>

      <div className="flex flex-col gap-2">
        <Label>访问审计</Label>
        <div className="flex flex-col gap-1">
          {access.length === 0 && <p className="text-muted-foreground text-xs">暂无记录</p>}
          {access.map((a) => (
            <div
              key={a.id}
              className="flex flex-wrap items-baseline gap-2 border-border border-b py-1 text-xs last:border-0"
            >
              <span className="font-mono">{a.action}</span>
              <Badge variant="outline">{a.actor_kind === "agent" ? "Agent" : "用户"}</Badge>
              <span>{a.actor}</span>
              {a.task_id ? <span>任务 #{a.task_id}</span> : null}
              <span className="text-muted-foreground">{a.reason || "—"}</span>
              <span className="ml-auto text-muted-foreground">{fmtTime(a.created_at)}</span>
            </div>
          ))}
        </div>
      </div>

      <Dialog open={revealOpen} onOpenChange={setRevealOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>显示明文</DialogTitle>
            <DialogDescription>
              本次操作会写入访问审计（操作者、时间与原因）。请在用完后立即隐藏，不要把值粘贴到报告或对话中。
            </DialogDescription>
          </DialogHeader>
          <div className="flex flex-col gap-2">
            <Label htmlFor="reveal-reason">取用原因</Label>
            <Textarea
              id="reveal-reason"
              rows={2}
              placeholder="如：用该账号登录后台验证越权"
              value={reason}
              onChange={(e) => setReason(e.target.value)}
            />
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setRevealOpen(false)} disabled={revealing}>
              取消
            </Button>
            <Button onClick={() => void reveal()} disabled={revealing}>
              {revealing ? "读取中…" : "确认显示"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

const LINK_LABEL: Record<SecretLinkTarget, string> = {
  task: "任务",
  asset: "资产",
  finding: "发现",
  company: "企业",
  host: "host",
  conversation: "会话",
};

function LinkRow({ link, onRemove }: { link: SecretLink; onRemove: () => Promise<void> }) {
  return (
    <div className="flex items-center gap-2 text-sm">
      <Badge variant="outline">{LINK_LABEL[link.target_type] ?? link.target_type}</Badge>
      <span className="font-mono text-xs">{link.target_ref}</span>
      <Button variant="ghost" size="icon" aria-label="解除关联" onClick={() => void onRemove()}>
        <UnlinkIcon />
      </Button>
    </div>
  );
}

function Field({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex flex-col gap-0.5">
      <span className="text-muted-foreground text-xs">{label}</span>
      <span className="text-sm">{value}</span>
    </div>
  );
}

function fmtTime(v?: string | null): string {
  if (!v) return "—";
  const d = new Date(v);
  if (Number.isNaN(d.getTime())) return "—";
  return d.toLocaleString();
}
