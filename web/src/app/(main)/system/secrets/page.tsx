"use client";

import * as React from "react";

import { KeyRoundIcon, PlusIcon, RefreshCwIcon, Trash2Icon } from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { api } from "@/lib/api";
import type { Secret, SecretKind, SecretsMeta } from "@/lib/types";

import { KIND_LABEL } from "./_components/kind-label";
import { SecretDetail } from "./_components/secret-detail";
import { SecretForm, type SecretFormState } from "./_components/secret-form";

const ALL = "all";

// 本页只做编排：加载列表、筛选、打开详情/新建抽屉。
// 列表与详情都不会拿到明文；明文只在详情里通过 reveal（需填原因）取得。
export default function SecretsPage() {
  const [meta, setMeta] = React.useState<SecretsMeta | null>(null);
  const [items, setItems] = React.useState<Secret[]>([]);
  const [loading, setLoading] = React.useState(false);
  const [q, setQ] = React.useState("");
  const [kind, setKind] = React.useState<string>(ALL);

  const [detailId, setDetailId] = React.useState<number | null>(null);
  const [createOpen, setCreateOpen] = React.useState(false);

  const load = React.useCallback(async () => {
    setLoading(true);
    try {
      const list = await api.secrets({ q: q.trim() || undefined, kind: kind === ALL ? undefined : kind });
      setItems(list);
    } catch (err) {
      toast.error("读取机密库失败", { description: (err as Error).message });
    } finally {
      setLoading(false);
    }
  }, [q, kind]);

  React.useEffect(() => {
    // 元数据失败不影响列表：类型筛选会回退到前端内置枚举。
    api.secretsMeta().then(setMeta, () => setMeta(null));
  }, []);

  React.useEffect(() => {
    void load();
  }, [load]);

  const remove = async (s: Secret) => {
    if (!window.confirm(`删除机密「${s.name}」？关联会被移除，访问审计保留。`)) return;
    try {
      await api.deleteSecret(s.id);
      toast.success("已删除");
      setDetailId(null);
      void load();
    } catch (err) {
      toast.error("删除失败", { description: (err as Error).message });
    }
  };

  return (
    <div className="flex flex-col gap-4 p-4 md:p-6">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="font-semibold text-xl">机密库</h1>
          <p className="text-muted-foreground text-sm">
            集中保存任务中发现的 key、密码与 token。列表只显示掩码，取用明文会记录原因与操作者。
          </p>
        </div>
        <div className="flex gap-2">
          <Button variant="outline" onClick={() => void load()} disabled={loading}>
            <RefreshCwIcon data-icon="inline-start" className={loading ? "animate-spin" : undefined} />
            刷新
          </Button>
          <Button onClick={() => setCreateOpen(true)}>
            <PlusIcon data-icon="inline-start" />
            新建
          </Button>
        </div>
      </div>

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <KeyRoundIcon className="size-4" />共 {items.length} 条
          </CardTitle>
          <CardDescription>同一类型、同一值的凭据只保留一条，多个任务共享同一条记录。</CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          <div className="flex flex-wrap gap-2">
            <Input
              className="max-w-sm"
              placeholder="按名称、说明、host 或来源搜索"
              value={q}
              onChange={(e) => setQ(e.target.value)}
            />
            <Select value={kind} onValueChange={setKind}>
              <SelectTrigger className="w-48">
                <SelectValue placeholder="全部类型" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={ALL}>全部类型</SelectItem>
                {(meta?.kinds ?? (Object.keys(KIND_LABEL) as SecretKind[])).map((k) => (
                  <SelectItem key={k} value={k}>
                    {KIND_LABEL[k]}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>名称</TableHead>
                <TableHead>类型</TableHead>
                <TableHead>值</TableHead>
                <TableHead>host</TableHead>
                <TableHead>来源</TableHead>
                <TableHead className="text-right">关联</TableHead>
                <TableHead>最后使用</TableHead>
                <TableHead className="text-right">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={8} className="py-8 text-center text-muted-foreground">
                    {loading ? "加载中…" : "暂无机密。Agent 在任务中发现凭据后会自动出现在这里，也可以手动新建。"}
                  </TableCell>
                </TableRow>
              ) : (
                items.map((s) => (
                  <TableRow key={s.id} className="cursor-pointer" onClick={() => setDetailId(s.id)}>
                    <TableCell className="font-medium">{s.name}</TableCell>
                    <TableCell>
                      <Badge variant="secondary">{KIND_LABEL[s.kind] ?? s.kind}</Badge>
                    </TableCell>
                    <TableCell className="font-mono text-muted-foreground text-xs">{s.value}</TableCell>
                    <TableCell>{s.host || "—"}</TableCell>
                    <TableCell className="max-w-56 truncate text-xs">{s.source || "—"}</TableCell>
                    <TableCell className="text-right">{s.link_count}</TableCell>
                    <TableCell className="text-xs">{fmtTime(s.last_used_at)}</TableCell>
                    <TableCell className="text-right">
                      <Button
                        variant="ghost"
                        size="icon"
                        aria-label="删除"
                        onClick={(e) => {
                          e.stopPropagation();
                          void remove(s);
                        }}
                      >
                        <Trash2Icon />
                      </Button>
                    </TableCell>
                  </TableRow>
                ))
              )}
            </TableBody>
          </Table>
        </CardContent>
      </Card>

      <Sheet open={detailId !== null} onOpenChange={(open) => !open && setDetailId(null)}>
        <SheetContent side="right" className="w-full overflow-y-auto data-[side=right]:sm:max-w-2xl">
          <SheetHeader>
            <SheetTitle>机密详情</SheetTitle>
            <SheetDescription>明文只在填写原因并确认后短暂显示。</SheetDescription>
          </SheetHeader>
          {detailId !== null && (
            <SecretDetail
              key={detailId}
              id={detailId}
              onChanged={() => void load()}
              onDeleted={() => {
                setDetailId(null);
                void load();
              }}
            />
          )}
        </SheetContent>
      </Sheet>

      <Sheet open={createOpen} onOpenChange={setCreateOpen}>
        <SheetContent side="right" className="w-full overflow-y-auto data-[side=right]:sm:max-w-lg">
          <SheetHeader>
            <SheetTitle>新建机密</SheetTitle>
            <SheetDescription>值只提交一次，保存后不可再读取（需通过「显示明文」并记录原因）。</SheetDescription>
          </SheetHeader>
          <SecretForm
            kinds={meta?.kinds}
            onCancel={() => setCreateOpen(false)}
            onSubmit={async (form: SecretFormState) => {
              await api.createSecret({
                name: form.name,
                kind: form.kind,
                value: form.value,
                host: form.host || undefined,
                notes: form.notes || undefined,
                source: form.source || undefined,
              });
              setCreateOpen(false);
              void load();
            }}
          />
        </SheetContent>
      </Sheet>
    </div>
  );
}

function fmtTime(v?: string | null): string {
  if (!v) return "—";
  const d = new Date(v);
  if (Number.isNaN(d.getTime())) return "—";
  return d.toLocaleString();
}
