"use client";

import * as React from "react";

import { DownloadIcon, PlusIcon, RefreshCwIcon, Trash2Icon } from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { api } from "@/lib/api";
import type { LibSnapshot, LibSourceInfo, LibSyncItem } from "@/lib/types";

/**
 * 技能来源与同步：git 型把整仓拉到 data/skills/git/<源名>（目录是远程镜像，本地改动会被
 * 覆盖），github/http 型读一份 zip 清单逐个安装到 data/skills/uploaded。
 *
 * 两个入口放在一起：顶栏的「同步」一键跑全部源，「来源」打开配置。令牌只进不出，
 * 页面只显示后端回的 has_token。
 */
export function SkillSyncControls() {
  const [open, setOpen] = React.useState(false);
  const [sources, setSources] = React.useState<LibSourceInfo[]>([]);
  const [roots, setRoots] = React.useState<string[]>([]);
  const [busy, setBusy] = React.useState(false);
  const [items, setItems] = React.useState<LibSyncItem[]>([]);
  const [snaps, setSnaps] = React.useState<LibSnapshot[]>([]);
  const [form, setForm] = React.useState({
    name: "",
    type: "git",
    url: "",
    repo: "",
    branch: "main",
    path: "manifest.json",
    token: "",
    allow_unpinned: false,
  });

  const load = React.useCallback(() => {
    api
      .skillSources()
      .then((r) => {
        setSources(r.sources);
        setRoots(r.roots);
      })
      .catch(() => {});
    api
      .skillSnapshots()
      .then(setSnaps)
      .catch(() => {});
  }, []);

  React.useEffect(() => {
    if (open) load();
  }, [open, load]);

  function setF(patch: Partial<typeof form>) {
    setForm((f) => ({ ...f, ...patch }));
  }

  function save() {
    if (!form.name.trim()) {
      toast.error("请先填源名（小写字母/数字/短横线）");
      return;
    }
    setBusy(true);
    api
      .saveSkillSource({
        source: {
          name: form.name.trim(),
          type: form.type,
          repo: form.repo.trim(),
          branch: form.branch.trim(),
          path: form.path.trim(),
          url: form.url.trim(),
          allow_unpinned: form.allow_unpinned,
        },
        token: form.token.trim() || undefined,
      })
      .then(() => {
        toast.success(`源 ${form.name} 已保存`);
        setForm({ name: "", type: "git", url: "", repo: "", branch: "main", path: "manifest.json", token: "", allow_unpinned: false });
        load();
      })
      .catch((e) => toast.error("保存失败：" + (e as Error).message))
      .finally(() => setBusy(false));
  }

  function remove(name: string) {
    if (!window.confirm(`移除源 ${name}？`)) return;
    const purge = window.confirm("是否同时删除它同步下来的目录/skill？\n\n（取消 = 只移除源配置）");
    setBusy(true);
    api
      .deleteSkillSource(name, purge)
      .then(() => {
        toast.success(`已移除源 ${name}`);
        load();
      })
      .catch((e) => toast.error("移除失败：" + (e as Error).message))
      .finally(() => setBusy(false));
  }

  function rollback(id?: string) {
    const label = id ? `快照 ${id}` : "最近一份快照";
    if (!window.confirm(`回滚技能库到${label}？\n\n覆盖安装与 git 更新过的 skill 会退回；之后新增的 skill 不受影响。`)) return;
    setBusy(true);
    api
      .skillRollback(id)
      .then((r) => {
        toast.success(`已回滚到 ${r.snapshot}（还原 ${r.restored.length} 项）`);
        load();
      })
      .catch((e) => toast.error("回滚失败：" + (e as Error).message))
      .finally(() => setBusy(false));
  }

  function sync(dryRun: boolean) {
    setBusy(true);
    api
      .skillSync({ dry_run: dryRun })
      .then((r) => {
        setItems(r.items);
        const head = dryRun ? "预览" : "同步";
        if (r.failed) toast.warning(`${head}完成：变更 ${r.changed}，失败 ${r.failed}`);
        else toast.success(`${head}完成：变更 ${r.changed} 条`);
        if (!dryRun) window.location.reload();
      })
      .catch((e) => toast.error("同步失败：" + (e as Error).message))
      .finally(() => setBusy(false));
  }

  return (
    <div className="flex items-center gap-2">
      <Button size="sm" variant="outline" onClick={() => sync(false)} disabled={busy}>
        <RefreshCwIcon className={busy ? "size-3.5 animate-spin" : "size-3.5"} />
        同步
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogTrigger asChild>
          <Button size="sm" variant="outline">
            <DownloadIcon className="size-3.5" />
            来源
          </Button>
        </DialogTrigger>
        <DialogContent className="sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle>技能来源</DialogTitle>
            <DialogDescription>
              git 型把整个仓库同步到 data/skills/git/&lt;源名&gt;（目录是远程镜像，本地改动会被覆盖）；github / http 型读一份
              zip 清单逐个安装。同步回退靠 manifest.json.bak，安装失败会保留原有 skill。
            </DialogDescription>
          </DialogHeader>

          <div className="space-y-3">
            {roots.length > 0 && (
              <p className="text-muted-foreground text-xs">
                扫描根：{roots.map((r) => (
                  <span key={r} className="mr-2 font-mono">
                    {r}
                  </span>
                ))}
              </p>
            )}

            {sources.length > 0 && (
              <div className="space-y-1">
                {sources.map((s) => (
                  <div key={s.name} className="flex items-center gap-2 rounded-md border p-2 text-xs">
                    <span className="font-medium">{s.name}</span>
                    <Badge variant="outline">{s.type}</Badge>
                    <span className="min-w-0 flex-1 truncate font-mono" title={s.target_dir || s.manifest_url}>
                      {s.config_error ? <span className="text-destructive">{s.config_error}</span> : s.target_dir || s.manifest_url}
                    </span>
                    {s.has_token && <Badge variant="secondary">令牌</Badge>}
                    <span className="shrink-0 text-muted-foreground">
                      {s.last_error ? <span className="text-destructive">{s.last_error}</span> : s.last_sync_at || "未同步"}
                    </span>
                    <Button variant="ghost" size="sm" onClick={() => remove(s.name)} disabled={busy}>
                      <Trash2Icon className="size-3.5" />
                    </Button>
                  </div>
                ))}
              </div>
            )}

            <div className="grid gap-3 md:grid-cols-2">
              <div className="space-y-1.5">
                <Label htmlFor="sk-src-name">源名</Label>
                <Input id="sk-src-name" value={form.name} onChange={(e) => setF({ name: e.target.value })} placeholder="skills-pack" />
              </div>
              <div className="space-y-1.5">
                <Label>类型</Label>
                <Select value={form.type} onValueChange={(v) => setF({ type: v })}>
                  <SelectTrigger className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="git">git（整仓）</SelectItem>
                    <SelectItem value="github">github（zip 清单）</SelectItem>
                    <SelectItem value="http">http（zip 清单）</SelectItem>
                  </SelectContent>
                </Select>
              </div>
              <div className="space-y-1.5 md:col-span-2">
                <Label htmlFor="sk-src-url">仓库 / 清单 URL</Label>
                <Input
                  id="sk-src-url"
                  value={form.url}
                  onChange={(e) => setF({ url: e.target.value })}
                  placeholder="https://github.com/org/artex-skills.git"
                />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="sk-src-repo">仓库 owner/repo（github 型）</Label>
                <Input id="sk-src-repo" value={form.repo} onChange={(e) => setF({ repo: e.target.value })} placeholder="org/skills" />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="sk-src-branch">分支</Label>
                <Input id="sk-src-branch" value={form.branch} onChange={(e) => setF({ branch: e.target.value })} placeholder="main" />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="sk-src-path">清单路径（github 型）</Label>
                <Input id="sk-src-path" value={form.path} onChange={(e) => setF({ path: e.target.value })} placeholder="manifest.json" />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="sk-src-token">访问令牌（可选）</Label>
                <Input id="sk-src-token" type="password" value={form.token} onChange={(e) => setF({ token: e.target.value })} />
              </div>
              <label className="flex items-center gap-2 text-sm md:col-span-2">
                <Checkbox checked={form.allow_unpinned} onCheckedChange={(v) => setF({ allow_unpinned: v === true })} />
                允许清单里未钉 sha256 的 zip（默认拒绝：同步链路必须可校验）
              </label>
            </div>

            <div className="flex flex-wrap gap-2">
              <Button size="sm" variant="outline" onClick={save} disabled={busy}>
                <PlusIcon className="size-4" />
                保存源
              </Button>
              <Button size="sm" variant="outline" onClick={() => sync(true)} disabled={busy || !sources.length}>
                预览变更
              </Button>
              <Button size="sm" onClick={() => sync(false)} disabled={busy || !sources.length}>
                <RefreshCwIcon className="size-4" />
                同步全部源
              </Button>
            </div>

            <div className="space-y-2 rounded-md border p-2">
              <div className="flex items-center justify-between gap-2">
                <span className="text-xs font-medium">快照与回滚</span>
                <Button size="sm" variant="ghost" onClick={() => rollback()} disabled={busy || !snaps.length}>
                  <RefreshCwIcon className="size-3.5" />
                  回滚到最近一份
                </Button>
              </div>
              {snaps.length === 0 ? (
                <p className="text-muted-foreground text-xs">
                  还没有快照：覆盖安装、git 更新、清源之前都会自动生成一份（含清单，最多保留 5 份）。
                </p>
              ) : (
                snaps.map((s) => (
                  <div key={s.id} className="flex flex-wrap items-center gap-2 text-xs">
                    <span className="font-mono">{s.id}</span>
                    <Badge variant="outline">{s.reason || "snapshot"}</Badge>
                    <span className="text-muted-foreground">{s.items.length} 项</span>
                    <Button variant="ghost" size="sm" className="ml-auto" onClick={() => rollback(s.id)} disabled={busy}>
                      回滚
                    </Button>
                  </div>
                ))
              )}
            </div>

            {items.length > 0 && (
              <div className="max-h-56 space-y-1 overflow-auto rounded-md border p-2">
                {items.map((it, i) => (
                  <div key={`${it.source}-${it.name}-${i}`} className="flex items-start gap-2 text-xs">
                    <Badge
                      variant={it.action === "failed" ? "destructive" : it.action === "keep" ? "outline" : "secondary"}
                    >
                      {it.action}
                    </Badge>
                    <span className="font-mono">
                      {it.source}/{it.name}
                    </span>
                    <span className="text-muted-foreground">
                      {it.error || ""}
                      {it.from_revision || it.to_revision
                        ? ` （${(it.from_revision || "-").slice(0, 8)} → ${(it.to_revision || "-").slice(0, 8)}）`
                        : ""}
                    </span>
                  </div>
                ))}
              </div>
            )}
          </div>
        </DialogContent>
      </Dialog>
    </div>
  );
}
