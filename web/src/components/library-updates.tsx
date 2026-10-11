"use client";

import * as React from "react";

import { toast } from "sonner";

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { api } from "@/lib/api";
import type { LibraryUpdateState, LibSnapshot } from "@/lib/types";

const labels = { add: "可安装", update: "可更新", keep: "已是最新", conflict: "本地冲突", unsupported: "平台不支持" };

export function LibraryUpdates({ kind, onUpdated }: { kind: "tools" | "skills"; onUpdated: () => void }) {
  const [state, setState] = React.useState<LibraryUpdateState | null>(null);
  const [error, setError] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const [selected, setSelected] = React.useState<string[]>([]);
  const [replace, setReplace] = React.useState<string[]>([]);
  const [snapshots, setSnapshots] = React.useState<LibSnapshot[]>([]);
  const [rollback, setRollback] = React.useState("");
  const [confirm, setConfirm] = React.useState(false);

  const load = React.useCallback(
    async (check = false) => {
      const result = await api.libraryUpdates(kind, check);
      setState(result);
      setSelected([]);
      setReplace([]);
      setSnapshots(await (kind === "tools" ? api.toolLibSnapshots() : api.skillSnapshots()));
    },
    [kind],
  );

  React.useEffect(() => {
    load().catch((e: Error) => setError(e.message));
  }, [load]);

  async function run(action: () => Promise<void>) {
    setBusy(true);
    setError("");
    try {
      await action();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  const toggle = (list: string[], name: string) =>
    list.includes(name) ? list.filter((n) => n !== name) : [...list, name];
  const enabled = selected.filter((name) =>
    state?.items.some((item) => item.name === name && (item.status !== "conflict" || replace.includes(name))),
  );

  return (
    <details className="shrink-0 rounded-lg border bg-card p-3" open={kind === "tools"}>
      <summary className="cursor-pointer font-medium text-sm">官方云更新 · {state?.release || "尚未检查"}</summary>
      <div className="mt-3 space-y-3">
        <div className="flex flex-wrap items-center gap-3">
          <Button size="sm" variant="outline" disabled={busy} onClick={() => run(() => load(true))}>
            {busy ? "处理中…" : "立即检查"}
          </Button>
          <label htmlFor={`registry-auto-${kind}`} className="flex items-center gap-2 text-sm">
            <Checkbox
              id={`registry-auto-${kind}`}
              checked={state ? !state.disabled : false}
              disabled={busy || !state}
              onCheckedChange={(value) =>
                run(async () => {
                  await api.libraryUpdateSettings(value !== true);
                  await load();
                })
              }
            />
            每 6 小时自动检查
          </label>
          <span className="text-muted-foreground text-xs">
            {state?.platform} · {state?.checked_at ? new Date(state.checked_at).toLocaleString() : "等待首次检查"}
          </span>
        </div>
        <p className="text-muted-foreground text-xs">
          自动检查只读取清单。选择条目后更新，应用和回滚需要任务与聊天空闲。
        </p>
        {(error || state?.error) && (
          <p role="alert" className="text-destructive text-sm">
            {error || state?.error}
          </p>
        )}
        {state && state.items.length === 0 && (
          <p className="text-muted-foreground text-sm">暂无更新清单，请点击“立即检查”。</p>
        )}
        {!!state?.items.length && (
          <div className="max-h-72 overflow-auto">
            <table className="w-full text-left text-sm">
              <thead>
                <tr className="border-b">
                  <th className="p-2">选择</th>
                  <th className="p-2">条目</th>
                  <th className="p-2">本地 → 云端</th>
                  <th className="p-2">状态与更新内容</th>
                </tr>
              </thead>
              <tbody>
                {state.items.map((item) => (
                  <tr key={item.name} className="border-b align-top">
                    <td className="p-2">
                      <Checkbox
                        aria-label={`选择 ${item.name}`}
                        disabled={busy || item.status === "unsupported" || item.status === "keep"}
                        checked={selected.includes(item.name)}
                        onCheckedChange={() => setSelected(toggle(selected, item.name))}
                      />
                    </td>
                    <td className="p-2">
                      <span className="font-medium">{item.name}</span>
                      <p className="text-muted-foreground text-xs">{item.description}</p>
                      <p className="text-muted-foreground text-xs">{item.assets.map((a) => a.platform).join("、")}</p>
                    </td>
                    <td className="whitespace-nowrap p-2">
                      {item.local_version || "未安装"} → {item.version}
                    </td>
                    <td className="min-w-48 p-2">
                      <p>
                        {labels[item.status]}
                        {item.reason && `：${item.reason}`}
                      </p>
                      <p className="text-muted-foreground text-xs">{item.changes}</p>
                      {item.shadowed && (
                        <p className="text-muted-foreground text-xs">显式本地 Skill 目录仍优先生效。</p>
                      )}
                      {item.status === "conflict" && (
                        <label
                          htmlFor={`registry-replace-${kind}-${item.name}`}
                          className="mt-2 flex items-center gap-2 text-xs"
                        >
                          <Checkbox
                            id={`registry-replace-${kind}-${item.name}`}
                            disabled={busy}
                            checked={replace.includes(item.name)}
                            onCheckedChange={() => setReplace(toggle(replace, item.name))}
                          />
                          允许用官方版本替换此条目
                        </label>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        <div className="flex flex-wrap items-center gap-2">
          <Button
            size="sm"
            disabled={busy || !enabled.length}
            onClick={() => {
              setRollback("");
              setConfirm(true);
            }}
          >
            更新所选 {enabled.length} 项
          </Button>
          <select
            aria-label="选择回滚快照"
            className="max-w-full rounded-md border bg-background p-1.5 text-sm"
            value={rollback}
            disabled={busy}
            onChange={(e) => setRollback(e.target.value)}
          >
            <option value="">选择回滚快照</option>
            {snapshots.map((snapshot) => (
              <option key={snapshot.id} value={snapshot.id}>
                {snapshot.created_at} · {snapshot.reason}
              </option>
            ))}
          </select>
          <Button size="sm" variant="outline" disabled={busy || !rollback} onClick={() => setConfirm(true)}>
            回滚
          </Button>
        </div>
      </div>
      <AlertDialog open={confirm} onOpenChange={setConfirm}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {rollback ? `回滚${kind === "tools" ? "工具库" : " Skill"}版本` : `应用 ${state?.release}`}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {rollback
                ? "将恢复所选快照的文件与清单。当前版本会先保存为新快照。"
                : `更新 ${enabled.join("、")}。已勾选替换的冲突条目将采用官方版本，当前版本会保留快照。`}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction
              onClick={() =>
                run(async () => {
                  if (rollback) {
                    await (kind === "tools" ? api.toolLibRollback(rollback) : api.skillRollback(rollback));
                    toast.success("已恢复快照");
                    setRollback("");
                  } else if (state) {
                    const result = await api.applyLibraryUpdates(
                      kind,
                      state.release,
                      enabled,
                      replace.filter((n) => enabled.includes(n)),
                    );
                    toast.success(
                      `已更新 ${result.changed} 项${result.skipped.length ? `，跳过 ${result.skipped.length} 项冲突` : ""}`,
                    );
                  }
                  await load();
                  onUpdated();
                })
              }
            >
              确认{rollback ? "回滚" : "更新"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </details>
  );
}
