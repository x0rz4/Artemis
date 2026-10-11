"use client";

import * as React from "react";

import {
  CheckCircle2Icon,
  DownloadIcon,
  FolderOpenIcon,
  PlusIcon,
  RefreshCwIcon,
  RotateCcwIcon,
  Trash2Icon,
  TriangleAlertIcon,
  UploadIcon,
} from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { api } from "@/lib/api";
import type { LibSnapshot, LibSourceInfo, LibSyncItem, ToolLib, ToolLibCheck } from "@/lib/types";

/** 快照占用展示（不足 1KB 按 B 显示）。 */
function fmtBytes(n: number): string {
  if (!n) return "0 B";
  const units = ["B", "KB", "MB", "GB"];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
}

/** kind/runtime 的下拉用哨兵值表示"不指定"，提交前换回空串。 */
const KIND_AUTO = "auto";
const RUNTIME_NATIVE = "native";

type UploadForm = {
  file: File | null;
  name: string;
  kind: string;
  runtime: string;
  entry: string;
  purpose: string;
};

const emptyForm: UploadForm = {
  file: null,
  name: "",
  kind: KIND_AUTO,
  runtime: RUNTIME_NATIVE,
  entry: "",
  purpose: "",
};

/** 源的编辑表单。令牌只进不出：后端只回 has_token，改的时候要重新填。 */
type SourceForm = {
  name: string;
  type: string;
  repo: string;
  branch: string;
  path: string;
  url: string;
  token: string;
  allow_unpinned: boolean;
};

const emptySource: SourceForm = {
  name: "",
  type: "github",
  repo: "",
  branch: "main",
  path: "manifest.json",
  url: "",
  token: "",
  allow_unpinned: false,
};

function actionBadge(action: LibSyncItem["action"]) {
  switch (action) {
    case "add":
      return <Badge>新增</Badge>;
    case "update":
      return <Badge variant="secondary">更新</Badge>;
    case "keep":
      return <Badge variant="outline">已最新</Badge>;
    default:
      return <Badge variant="destructive">失败</Badge>;
  }
}

/** 完整性状态 → 徽标。unpinned 是"能跑但不校验"，用中性色避免误导。 */
function ShaBadge({ check }: { check: ToolLibCheck }) {
  switch (check.sha256_state) {
    case "ok":
      return (
        <Badge variant="secondary" className="gap-1">
          <CheckCircle2Icon className="size-3" />
          已校验
        </Badge>
      );
    case "mismatch":
      return (
        <Badge variant="destructive" className="gap-1">
          <TriangleAlertIcon className="size-3" />
          摘要不符
        </Badge>
      );
    case "missing":
      return (
        <Badge variant="destructive" className="gap-1">
          <TriangleAlertIcon className="size-3" />
          文件缺失
        </Badge>
      );
    case "unpinned":
      return (
        <Badge variant="outline" className="gap-1">
          未钉摘要
        </Badge>
      );
    default:
      return (
        <Badge variant="outline" className="gap-1">
          目录
        </Badge>
      );
  }
}

export default function ToolLibPage() {
  const [lib, setLib] = React.useState<ToolLib | null>(null);
  const [loading, setLoading] = React.useState(true);
  const [busy, setBusy] = React.useState(false);
  const [form, setForm] = React.useState<UploadForm>(emptyForm);
  const [sources, setSources] = React.useState<LibSourceInfo[]>([]);
  const [src, setSrc] = React.useState<SourceForm>(emptySource);
  const [syncItems, setSyncItems] = React.useState<LibSyncItem[]>([]);
  const [snaps, setSnaps] = React.useState<LibSnapshot[]>([]);

  const load = React.useCallback(
    (quiet = false) => {
      if (!quiet) setLoading(true);
      api
        .toolLib()
        .then(setLib)
        .catch((e) => toast.error("读取工具库失败：" + (e as Error).message))
        .finally(() => setLoading(false));
      api
        .toolLibSources()
        .then(setSources)
        .catch(() => {});
      api
        .toolLibSnapshots()
        .then(setSnaps)
        .catch(() => {});
    },
    [],
  );

  React.useEffect(() => {
    load();
  }, [load]);

  function setF(patch: Partial<UploadForm>) {
    setForm((f) => ({ ...f, ...patch }));
  }

  function setS(patch: Partial<SourceForm>) {
    setSrc((s) => ({ ...s, ...patch }));
  }

  function saveSource() {
    if (!src.name.trim()) {
      toast.error("请先填源名（小写字母/数字/短横线）");
      return;
    }
    setBusy(true);
    api
      .saveToolLibSource({
        source: {
          name: src.name.trim(),
          type: src.type,
          repo: src.repo.trim(),
          branch: src.branch.trim(),
          path: src.path.trim(),
          url: src.url.trim(),
          allow_unpinned: src.allow_unpinned,
        },
        token: src.token.trim() || undefined,
      })
      .then(() => {
        toast.success(`源 ${src.name} 已保存`);
        setSrc(emptySource);
        load(true);
      })
      .catch((e) => toast.error("保存失败：" + (e as Error).message))
      .finally(() => setBusy(false));
  }

  function removeSource(name: string) {
    if (!window.confirm(`移除源 ${name}？\n\n已同步下来的工具与本地清单条目都会保留。`)) return;
    setBusy(true);
    api
      .deleteToolLibSource(name)
      .then(() => {
        toast.success(`已移除源 ${name}`);
        load(true);
      })
      .catch((e) => toast.error("移除失败：" + (e as Error).message))
      .finally(() => setBusy(false));
  }

  // 回滚把磁盘文件与清单一起退回某份快照（快照之后新增的条目不动）。
  function rollback(id?: string) {
    const label = id ? `快照 ${id}` : "最近一份快照";
    if (!window.confirm(`回滚到${label}？\n\n磁盘文件与清单会一起退回；快照之后新增的工具不受影响。`)) return;
    setBusy(true);
    api
      .toolLibRollback(id)
      .then((r) => {
        toast.success(`已回滚到 ${r.snapshot}（还原 ${r.restored.length} 项，注册 ${r.imported ?? 0} 个工具）`);
        load(true);
      })
      .catch((e) => toast.error("回滚失败：" + (e as Error).message))
      .finally(() => setBusy(false));
  }

  // dryRun 只回计划：先看清哪些条目会动，再决定要不要真同步。
  function runSync(dryRun: boolean) {
    setBusy(true);
    api
      .toolLibSync({ dry_run: dryRun })
      .then((r) => {
        setSyncItems(r.items);
        const head = dryRun ? "预览" : "同步";
        if (r.failed) toast.warning(`${head}完成：变更 ${r.changed}，失败 ${r.failed}`);
        else toast.success(`${head}完成：变更 ${r.changed} 条`);
        if (!dryRun) load(true);
      })
      .catch((e) => toast.error("同步失败：" + (e as Error).message))
      .finally(() => setBusy(false));
  }

  function doCheck() {
    setBusy(true);
    api
      .toolLibCheck()
      .then((sum) => {
        // 自检结果只有告警意义：不阻断任何操作，只把问题列出来。
        if (sum.missing || sum.mismatch || sum.runtime_missing) {
          toast.warning(
            `自检完成：缺失 ${sum.missing}，摘要不符 ${sum.mismatch}，runtime 缺失 ${sum.runtime_missing}`,
          );
        } else {
          toast.success(`自检完成：${sum.total} 个条目全部就绪`);
        }
        load(true);
      })
      .catch((e) => toast.error("自检失败：" + (e as Error).message))
      .finally(() => setBusy(false));
  }

  function doImport() {
    setBusy(true);
    api
      .toolLibImport()
      .then((r) => {
        const failed = r.results.filter((x) => x.error);
        if (failed.length) {
          toast.warning(`导入 ${r.imported} 条，${failed.length} 条失败：${failed[0].error}`);
        } else {
          toast.success(`导入完成：${r.imported} 条已注册为可调用工具`);
        }
        load(true);
      })
      .catch((e) => toast.error("导入失败：" + (e as Error).message))
      .finally(() => setBusy(false));
  }

  function submitUpload(overwrite: boolean) {
    // 取成局部变量：下面的回调里 TS 无法保持 form.file 的非空收窄。
    const file = form.file;
    if (!file) {
      toast.error("请先选择要上传的文件");
      return;
    }
    setBusy(true);
    api
      .uploadTool(
        file,
        {
          name: form.name.trim() || undefined,
          kind: form.kind === KIND_AUTO ? undefined : form.kind,
          runtime: form.runtime === RUNTIME_NATIVE ? undefined : form.runtime,
          entry: form.entry.trim() || undefined,
          purpose: form.purpose.trim() || undefined,
        },
        overwrite,
      )
      .then((r) => {
        toast.success(`${r.name} 已安装并注册为工具 ${r.key}`);
        setForm(emptyForm);
        load(true);
      })
      .catch((e) => {
        const msg = (e as Error).message;
        // 同名冲突：二次确认后带 overwrite 重试，避免顺手覆盖别人的工具。
        if (!overwrite && msg.includes("已存在同名工具") && window.confirm(`${msg}\n\n确认覆盖？`)) {
          setBusy(false);
          submitUpload(true);
          return;
        }
        toast.error("上传失败：" + msg);
      })
      .finally(() => setBusy(false));
  }

  function removeEntry(name: string) {
    if (!window.confirm(`从清单里移除 ${name}？\n\n磁盘文件与已注册的工具会保留。`)) return;
    const purge = window.confirm("是否同时删除磁盘文件和已注册的工具？\n\n（取消 = 只从清单移除）");
    setBusy(true);
    api
      .deleteToolLibEntry(name, purge)
      .then(() => {
        toast.success(purge ? `已删除 ${name}（含文件与工具）` : `已从清单移除 ${name}`);
        load(true);
      })
      .catch((e) => toast.error("移除失败：" + (e as Error).message))
      .finally(() => setBusy(false));
  }

  const sum = lib?.summary;

  return (
    <div className="flex flex-1 flex-col gap-4 md:gap-6">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 className="text-xl font-semibold tracking-tight">工具库</h1>
          <p className="text-muted-foreground text-sm">
            文件与清单是真源；导入后注册成 <span className="font-mono">kind=command</span> 的工具，执行仍走拦截规则与人工审批。
          </p>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button variant="outline" size="sm" onClick={doCheck} disabled={busy || loading}>
            <RefreshCwIcon className={busy ? "size-4 animate-spin" : "size-4"} />
            重新自检
          </Button>
          <Button size="sm" onClick={doImport} disabled={busy || loading}>
            <FolderOpenIcon className="size-4" />
            导入清单
          </Button>
        </div>
      </div>

      {lib && (
        <div className="space-y-1.5">
          <div className="flex flex-wrap items-center gap-2">
            <Badge variant="secondary">条目 {sum?.total ?? 0}</Badge>
            {!!sum?.missing && <Badge variant="destructive">文件缺失 {sum.missing}</Badge>}
            {!!sum?.mismatch && <Badge variant="destructive">摘要不符 {sum.mismatch}</Badge>}
            {!!sum?.runtime_missing && <Badge variant="destructive">runtime 缺失 {sum.runtime_missing}</Badge>}
            {!!sum?.unpinned && <Badge variant="outline">未钉摘要 {sum.unpinned}</Badge>}
            {!!lib.updated_at && (
              <span className="text-muted-foreground text-xs">清单更新于 {lib.updated_at}</span>
            )}
          </div>
          <p className="text-muted-foreground text-xs">
            真源：<span className="font-mono">{lib.manifest}</span> · 目录 <span className="font-mono">{lib.dir}</span>
          </p>
        </div>
      )}

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-base">
            <UploadIcon className="size-4" />
            上传工具
          </CardTitle>
          <CardDescription>
            单文件支持二进制 / Python·Node·PHP 脚本 / JAR；zip 适合目录型工具（多文件时必须给入口文件，或在包内放
            <span className="font-mono"> manifest-entry.json</span>）。上传即注册，可在「系统 → 工具」里调整绑定或停用。
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
            <div className="space-y-1.5">
              <Label htmlFor="tl-file">文件</Label>
              <Input
                id="tl-file"
                type="file"
                onChange={(e) => setF({ file: e.target.files?.[0] ?? null })}
                className="h-9"
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="tl-name">工具名（留空用文件名）</Label>
              <Input id="tl-name" value={form.name} onChange={(e) => setF({ name: e.target.value })} placeholder="fscan" />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="tl-purpose">用途说明</Label>
              <Input
                id="tl-purpose"
                value={form.purpose}
                onChange={(e) => setF({ purpose: e.target.value })}
                placeholder="内网综合扫描"
              />
            </div>
            <div className="space-y-1.5">
              <Label>类型</Label>
              <Select value={form.kind} onValueChange={(v) => setF({ kind: v })}>
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value={KIND_AUTO}>按内容推断</SelectItem>
                  <SelectItem value="binary">二进制</SelectItem>
                  <SelectItem value="script">脚本</SelectItem>
                  <SelectItem value="jar">JAR</SelectItem>
                  <SelectItem value="dir">目录型</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-1.5">
              <Label>Runtime</Label>
              <Select value={form.runtime} onValueChange={(v) => setF({ runtime: v })}>
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value={RUNTIME_NATIVE}>原生（不指定）</SelectItem>
                  <SelectItem value="python3">python3</SelectItem>
                  <SelectItem value="node">node</SelectItem>
                  <SelectItem value="php">php</SelectItem>
                  <SelectItem value="java">java</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="tl-entry">入口文件（zip 目录型工具）</Label>
              <Input
                id="tl-entry"
                value={form.entry}
                onChange={(e) => setF({ entry: e.target.value })}
                placeholder="dirsearch.py"
              />
            </div>
          </div>
          <Button size="sm" onClick={() => submitUpload(false)} disabled={busy || !form.file}>
            <UploadIcon className="size-4" />
            上传并注册
          </Button>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-base">
            <DownloadIcon className="size-4" />
            来源与同步
          </CardTitle>
          <CardDescription>
            github 型读仓库里的清单文件，http 型读任意静态清单；同步只增改条目，本地独有的工具不会被抹掉。令牌存在服务端设置里，页面只显示是否已配置。
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          {sources.length > 0 && (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>源</TableHead>
                  <TableHead>类型</TableHead>
                  <TableHead>位置</TableHead>
                  <TableHead>令牌</TableHead>
                  <TableHead>上次同步</TableHead>
                  <TableHead className="w-16" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {sources.map((s) => (
                  <TableRow key={s.name}>
                    <TableCell className="font-medium">{s.name}</TableCell>
                    <TableCell className="text-xs">{s.type}</TableCell>
                    <TableCell className="max-w-[22rem] truncate font-mono text-xs" title={s.manifest_url}>
                      {s.config_error ? <span className="text-destructive">{s.config_error}</span> : s.manifest_url}
                    </TableCell>
                    <TableCell className="text-xs">
                      {s.has_token ? <Badge variant="secondary">已配置</Badge> : <span className="text-muted-foreground">匿名</span>}
                    </TableCell>
                    <TableCell className="text-xs">
                      {s.last_error ? (
                        <span className="text-destructive">{s.last_error}</span>
                      ) : (
                        <span className="text-muted-foreground">{s.last_sync_at || "未同步"}</span>
                      )}
                    </TableCell>
                    <TableCell>
                      <Button variant="ghost" size="sm" onClick={() => removeSource(s.name)} disabled={busy}>
                        <Trash2Icon className="size-4" />
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}

          <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
            <div className="space-y-1.5">
              <Label htmlFor="src-name">源名</Label>
              <Input id="src-name" value={src.name} onChange={(e) => setS({ name: e.target.value })} placeholder="official" />
            </div>
            <div className="space-y-1.5">
              <Label>类型</Label>
              <Select value={src.type} onValueChange={(v) => setS({ type: v })}>
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="github">github</SelectItem>
                  <SelectItem value="http">http</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="src-repo">仓库 owner/repo（github）</Label>
              <Input id="src-repo" value={src.repo} onChange={(e) => setS({ repo: e.target.value })} placeholder="org/artex-tools" />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="src-branch">分支</Label>
              <Input id="src-branch" value={src.branch} onChange={(e) => setS({ branch: e.target.value })} placeholder="main" />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="src-path">清单路径（github）</Label>
              <Input id="src-path" value={src.path} onChange={(e) => setS({ path: e.target.value })} placeholder="manifest.json" />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="src-url">清单 URL（http）</Label>
              <Input id="src-url" value={src.url} onChange={(e) => setS({ url: e.target.value })} placeholder="https://dl.example.com/tools/manifest.json" />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="src-token">访问令牌（可选，留空不改）</Label>
              <Input
                id="src-token"
                type="password"
                value={src.token}
                onChange={(e) => setS({ token: e.target.value })}
                placeholder="私有源只读令牌"
              />
            </div>
            <label className="flex items-center gap-2 self-end pb-2 text-sm">
              <Checkbox checked={src.allow_unpinned} onCheckedChange={(v) => setS({ allow_unpinned: v === true })} />
              允许未钉 sha256 的条目
            </label>
          </div>

          <div className="flex flex-wrap gap-2">
            <Button size="sm" variant="outline" onClick={saveSource} disabled={busy}>
              <PlusIcon className="size-4" />
              保存源
            </Button>
            <Button size="sm" variant="outline" onClick={() => runSync(true)} disabled={busy || !sources.length}>
              预览变更
            </Button>
            <Button size="sm" onClick={() => runSync(false)} disabled={busy || !sources.length}>
              <DownloadIcon className="size-4" />
              同步全部源
            </Button>
          </div>

          <div className="space-y-2 rounded-md border p-2">
            <div className="flex items-center justify-between gap-2">
              <span className="text-xs font-medium">快照与回滚</span>
              <Button size="sm" variant="ghost" onClick={() => rollback()} disabled={busy || !snaps.length}>
                <RotateCcwIcon className="size-4" />
                回滚到最近一份
              </Button>
            </div>
            {snaps.length === 0 ? (
              <p className="text-muted-foreground text-xs">
                还没有快照：同步、覆盖上传、清除文件、回滚之前都会自动生成一份（含清单，最多保留 5 份）。
              </p>
            ) : (
              snaps.map((s) => (
                <div key={s.id} className="flex flex-wrap items-center gap-2 text-xs">
                  <span className="font-mono">{s.id}</span>
                  <Badge variant="outline">{s.reason || "snapshot"}</Badge>
                  <span className="text-muted-foreground">
                    {s.items.length} 项 · {fmtBytes(s.bytes)}
                  </span>
                  <Button variant="ghost" size="sm" className="ml-auto" onClick={() => rollback(s.id)} disabled={busy}>
                    <RotateCcwIcon className="size-3.5" />
                    回滚
                  </Button>
                </div>
              ))
            )}
          </div>

          {syncItems.length > 0 && (
            <div className="max-h-64 space-y-1 overflow-auto rounded-md border p-2">
              {syncItems.map((it, i) => (
                <div key={`${it.source}-${it.name}-${i}`} className="flex items-start gap-2 text-xs">
                  {actionBadge(it.action)}
                  <span className="font-mono">
                    {it.source}/{it.name}
                  </span>
                  <span className="text-muted-foreground">
                    {it.error || it.reason || ""}
                    {it.local_version || it.remote_version ? ` （${it.local_version || "-"} → ${it.remote_version || "-"}）` : ""}
                  </span>
                </div>
              ))}
            </div>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">清单条目</CardTitle>
          <CardDescription>
            缺文件、摘要不符、runtime 没装都只是告警——武器库不全时其它工具照常可用。
          </CardDescription>
        </CardHeader>
        <CardContent>
          {loading ? (
            <p className="text-muted-foreground text-sm">加载中…</p>
          ) : !lib?.tools.length ? (
            <p className="text-muted-foreground text-sm">
              清单还是空的：上传一个工具，或把已有的 <span className="font-mono">tools-manifest.json</span> 内容合并进{" "}
              <span className="font-mono">manifest.json</span> 后点「导入清单」。
            </p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>名称</TableHead>
                  <TableHead>来源</TableHead>
                  <TableHead>类型</TableHead>
                  <TableHead>路径</TableHead>
                  <TableHead>完整性</TableHead>
                  <TableHead>Runtime</TableHead>
                  <TableHead>注册</TableHead>
                  <TableHead className="w-20" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {lib.tools.map((t) => (
                  <TableRow key={t.name}>
                    <TableCell>
                      <div className="font-medium">{t.name}</div>
                      {t.purpose && <div className="text-muted-foreground text-xs">{t.purpose}</div>}
                    </TableCell>
                    <TableCell className="text-xs">
                      {t.source ? (
                        <div className="flex flex-col gap-1">
                          <Badge variant="outline">{t.source === "uploaded" ? "本地上传" : t.source}</Badge>
                          {t.version && <span className="text-muted-foreground">v{t.version}</span>}
                        </div>
                      ) : (
                        <span className="text-muted-foreground">-</span>
                      )}
                    </TableCell>
                    <TableCell className="text-xs">
                      {t.kind || "-"}
                      {t.entry ? <span className="text-muted-foreground"> / {t.entry}</span> : null}
                    </TableCell>
                    <TableCell className="font-mono text-xs">{t.path}</TableCell>
                    <TableCell>
                      <div className="flex flex-col gap-1">
                        <ShaBadge check={t.check} />
                        {t.check.problem && (
                          <span className="text-muted-foreground text-xs">{t.check.problem}</span>
                        )}
                      </div>
                    </TableCell>
                    <TableCell className="text-xs">
                      {t.runtime ? t.runtime : "原生"}
                      {t.runtime && !t.check.runtime_ok ? (
                        <span className="text-destructive">（未安装）</span>
                      ) : null}
                    </TableCell>
                    <TableCell className="text-xs">
                      {t.registered ? (
                        <div className="flex flex-col gap-1">
                          <Badge variant={t.enabled === false ? "outline" : "secondary"}>
                            {t.enabled === false ? "已停用" : "已注册"}
                          </Badge>
                          <span className="text-muted-foreground">{t.agents?.length ?? 0} 个 Agent</span>
                        </div>
                      ) : (
                        <Badge variant="outline">未注册</Badge>
                      )}
                    </TableCell>
                    <TableCell>
                      <Button variant="ghost" size="sm" onClick={() => removeEntry(t.name)} disabled={busy}>
                        <Trash2Icon className="size-4" />
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
