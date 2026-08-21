<!-- owner: muswood | Email: mumu920@outlook.com -->
<script lang="ts">
  import { Folder, File, RefreshCw, Home, ChevronRight, HardDrive, Server, Upload, Download, FolderPlus, Pencil, Eye, Save, X, Link } from "lucide-svelte";
  import { onMount } from "svelte";
  import { SFTPListDir, SFTPUpload, SFTPDownload, SFTPRemoveRecursive, SFTPMkdir, SFTPRename, SFTPCancelTransfer, SFTPReadFile, SFTPWriteFile, SFTPExtensions, SFTPDiskUsage, SFTPRealPath, SFTPChmod, SFTPSymlink } from "../../../wailsjs/go/main/App";
  import { SFTPLocalListDir, SFTPLocalHomeDir } from "../../../wailsjs/go/main/App";
  import { EventsOn } from "../../../wailsjs/runtime/runtime";
  import { language } from "$lib/stores";
  import { t } from "$lib/i18n";

  let { sessionId = "" } = $props<{ sessionId?: string }>();

  interface FEntry { name: string; size: number; isDir: boolean; perm: string; isSymlink?: boolean; linkTarget?: string; }
  let remotePath = $state("/");
  let localPath = $state("");
  let remoteFiles = $state<FEntry[]>([]);
  let localFiles = $state<FEntry[]>([]);
  let selectedRemote = $state<string | null>(null);
  let selectedLocal = $state<string | null>(null);
  let selectedRemoteFiles = $state<string[]>([]);
  let selectedLocalFiles = $state<string[]>([]);
  let localLoadVersion = 0;
  let remoteLoadVersion = 0;
  let transfer = $state<{
    id: string; type: string; fileName: string; total: number; done: number; percent: number; status: string;
    attempt?: number; resumed?: boolean; verified?: boolean;
  } | null>(null);
  type TransferItem = {
    id: string; type: string; fileName: string; total: number; done: number; percent: number; status: string;
    attempt?: number; resumed?: boolean; verified?: boolean;
  };
  type DragSide = "local" | "remote";
  type DragPayload = { side: DragSide; names: string[] };
  let transferQueue = $state<TransferItem[]>([]);
  let draggingSide = $state<DragSide | null>(null);
  let localDropActive = $state(false);
  let remoteDropActive = $state(false);
  let editorOpen = $state(false);
  let editorPath = $state("");
  let editorContent = $state("");
  let editorBusy = $state(false);
  let editorError = $state("");
  let remoteInfoOpen = $state(false);
  let remoteInfo = $state<any>(null);
  let remoteInfoError = $state("");
  let remoteError = $state("");
  let localError = $state("");
  let remoteLoading = $state(false);
  let localLoading = $state(false);
  let autoRefreshTimer: ReturnType<typeof setInterval> | null = null;
  let autoRefreshEnabled = $state(true);
  let autoRefreshSeconds = $state(5);
  let mounted = false;

  async function loadLocal(path = localPath): Promise<boolean> {
    const version = ++localLoadVersion;
    localLoading = true;
    localError = "";
    try {
      const json = await SFTPLocalListDir(path);
      if (version !== localLoadVersion) return false;
      localFiles = JSON.parse(json || "[]");
      localPath = path;
      return true;
    } catch (e: any) {
      if (version === localLoadVersion) localError = e?.message || String(e) || "无法读取本地目录";
      return false;
    } finally {
      if (version === localLoadVersion) localLoading = false;
    }
  }

  async function loadRemote(path = remotePath): Promise<boolean> {
    if (!sessionId) return false;
    const version = ++remoteLoadVersion;
    remoteLoading = true;
    remoteError = "";
    try {
      const json = await SFTPListDir(sessionId, path);
      if (version !== remoteLoadVersion) return false;
      remoteFiles = JSON.parse(json || "[]");
      remotePath = path;
      const names = new Set(remoteFiles.map((file) => file.name));
      selectedRemoteFiles = selectedRemoteFiles.filter((name) => names.has(name));
      selectedRemote = selectedRemoteFiles.at(-1) || null;
      return true;
    } catch (e: any) {
      if (version === remoteLoadVersion) remoteError = e?.message || String(e) || "无法读取远程目录";
      return false;
    } finally {
      if (version === remoteLoadVersion) remoteLoading = false;
    }
  }

  async function loadRemoteInfo() {
    if (!sessionId) return;
    remoteInfoError = "";
    try {
      const [extensionsRaw, realPath] = await Promise.all([
        SFTPExtensions(sessionId),
        SFTPRealPath(sessionId, remotePath),
      ]);
      let usage: any = null;
      try { usage = JSON.parse(await SFTPDiskUsage(sessionId, remotePath)); }
      catch (e) { usage = null; }
      remoteInfo = {
        extensions: JSON.parse(extensionsRaw || "{}"),
        usage,
        realPath,
      };
      remoteInfoOpen = true;
    } catch (e: any) {
      remoteInfoError = e?.toString?.() || "读取远端扩展信息失败";
      remoteInfoOpen = true;
    }
  }

  function formatSize(bytes: number): string {
    if (bytes === 0) return "—";
    const u = ["B","KB","MB","GB"]; let i = 0, s = bytes;
    while (s >= 1024 && i < u.length-1) { s /= 1024; i++; }
    return s.toFixed(1) + " " + u[i];
  }

  function joinPath(base: string, name: string): string {
    if (!base || base === "/") return "/" + name;
    return base.replace(/\/+$/, "") + "/" + name;
  }

  function baseName(path: string): string {
    return path.replace(/\\/g, "/").split("/").filter(Boolean).at(-1) || path;
  }

  function statusLabel(item: TransferItem): string {
    if (item.status === "failed") return "失败";
    if (item.status === "cancelled") return "已取消";
    if (item.status === "completed") return "完成";
    if (item.status === "retrying") return `重试 ${item.attempt || ""}/3`;
    if (item.status === "verifying") return "校验中";
    return "进行中";
  }

  function updateTransferQueue(data: any) {
    const item: TransferItem = {
      id: data.id,
      type: data.type || "transfer",
      fileName: baseName(data.fileName || ""),
      total: data.total || 0,
      done: data.done || 0,
      percent: data.percent || 0,
      status: data.status || "running",
      attempt: data.attempt,
      resumed: data.resumed,
      verified: data.verified,
    };
    transferQueue = [item, ...transferQueue.filter((existing) => existing.id !== item.id)].slice(0, 8);
  }

  async function enterRemote(f: FEntry) {
    if (!f.isDir || remoteLoading) return;
    const targetPath = joinPath(remotePath, f.name);
    selectedRemote = null;
    selectedRemoteFiles = [];
    await loadRemote(targetPath);
  }
  async function goRemoteUp() {
    if (remoteLoading || remotePath === "/") return;
    const p = remotePath.replace(/\/+$/, '').split('/'); p.pop();
    selectedRemote = null;
    selectedRemoteFiles = [];
    await loadRemote(p.join('/') || '/');
  }

  async function enterLocal(f: FEntry) {
    if (!f.isDir || localLoading) return;
    const targetPath = joinPath(localPath, f.name);
    selectedLocal = null;
    selectedLocalFiles = [];
    await loadLocal(targetPath);
  }
  async function goLocalUp() {
    if (localLoading || !localPath) return;
    const p = localPath.replace(/\/+$/, '').split('/'); p.pop();
    selectedLocal = null;
    selectedLocalFiles = [];
    await loadLocal(p.join('/') || '/');
  }

  function selectFile(side: "local" | "remote", name: string, event: MouseEvent) {
    const selected = side === "local" ? selectedLocalFiles : selectedRemoteFiles;
    const next = event.ctrlKey || event.metaKey
      ? (selected.includes(name) ? selected.filter((item) => item !== name) : [...selected, name])
      : [name];
    if (side === "local") {
      selectedLocalFiles = next;
      selectedLocal = next.at(-1) || null;
    } else {
      selectedRemoteFiles = next;
      selectedRemote = next.at(-1) || null;
    }
  }

  async function handleUpload(names = selectedLocalFiles) {
    if (!sessionId || names.length === 0) return;
    const conflicts = names.filter(name => remoteFiles.some(file => file.name === name));
    if (conflicts.length && !confirm(`远程已存在 ${conflicts.length} 个同名项目，继续后将覆盖：\n${conflicts.join("\n")}`)) return;
    try {
      for (const name of names) {
        await SFTPUpload(sessionId, joinPath(localPath, name), joinPath(remotePath, name));
      }
      await loadRemote();
    } catch (e: any) {
      remoteError = e?.message || String(e) || "上传失败";
      console.error("上传失败", e);
    }
  }

  async function uploadExternalFiles(files: globalThis.File[]) {
    if (!sessionId || files.length === 0) return;
    const items = files.map((file) => {
      const wf = file as unknown as { path?: string; webkitRelativePath?: string; name: string };
      return { path: wf.path || wf.webkitRelativePath || "", name: wf.name };
    });
    const conflicts = items.filter(item => remoteFiles.some(file => file.name === item.name)).map(item => item.name);
    if (conflicts.length && !confirm(`远程已存在同名项目，继续后将覆盖：\n${conflicts.join("\n")}`)) return;
    const missing = items.filter((item) => !item.path);
    if (missing.length) {
      remoteError = "当前运行环境没有暴露拖入文件的本地绝对路径，请从左侧本地文件列表拖拽上传";
      return;
    }
    try {
      for (const item of items) {
        await SFTPUpload(sessionId, item.path, joinPath(remotePath, item.name || baseName(item.path)));
      }
      await loadRemote();
    } catch (e: any) {
      remoteError = e?.message || String(e) || "拖拽上传失败";
    }
  }

  async function handleDownload(names = selectedRemoteFiles) {
    if (!sessionId || names.length === 0) return;
    const conflicts = names.filter(name => localFiles.some(file => file.name === name));
    if (conflicts.length && !confirm(`本地已存在 ${conflicts.length} 个同名项目，继续后将覆盖：\n${conflicts.join("\n")}`)) return;
    try {
      for (const name of names) {
        await SFTPDownload(sessionId, joinPath(remotePath, name), joinPath(localPath, name));
      }
      await loadLocal();
    } catch (e: any) {
      localError = e?.message || String(e) || "下载失败";
      console.error("下载失败", e);
    }
  }

  function dragNames(side: DragSide, name: string): string[] {
    const selected = side === "local" ? selectedLocalFiles : selectedRemoteFiles;
    return selected.includes(name) ? selected : [name];
  }

  function startDrag(side: DragSide, name: string, event: DragEvent) {
    const names = dragNames(side, name);
    draggingSide = side;
    const payload: DragPayload = { side, names };
    event.dataTransfer?.setData("application/x-gossh-sftp", JSON.stringify(payload));
    event.dataTransfer?.setData("text/plain", names.join("\n"));
    if (event.dataTransfer) event.dataTransfer.effectAllowed = "copy";
  }

  function endDrag() {
    draggingSide = null;
    localDropActive = false;
    remoteDropActive = false;
  }

  function parseDragPayload(event: DragEvent): DragPayload | null {
    const raw = event.dataTransfer?.getData("application/x-gossh-sftp");
    if (!raw) return null;
    try {
      const payload = JSON.parse(raw) as DragPayload;
      if ((payload.side === "local" || payload.side === "remote") && Array.isArray(payload.names)) return payload;
    } catch {}
    return null;
  }

  function handleDragOver(side: DragSide, event: DragEvent) {
    const hasFiles = Array.from(event.dataTransfer?.types || []).includes("Files");
    const canDrop = side === "remote" ? draggingSide === "local" || hasFiles : draggingSide === "remote";
    if (!canDrop) return;
    event.preventDefault();
    if (event.dataTransfer) event.dataTransfer.dropEffect = "copy";
    if (side === "remote") remoteDropActive = true;
    else localDropActive = true;
  }

  function handleDragLeave(side: DragSide, event: DragEvent) {
    const related = event.relatedTarget as Node | null;
    if (related && (event.currentTarget as HTMLElement).contains(related)) return;
    if (side === "remote") remoteDropActive = false;
    else localDropActive = false;
  }

  async function dropOnRemote(event: DragEvent) {
    event.preventDefault();
    remoteDropActive = false;
    const payload = parseDragPayload(event);
    if (payload?.side === "local") {
      await handleUpload(payload.names);
      endDrag();
      return;
    }
    const files = Array.from(event.dataTransfer?.files || []);
    if (files.length) await uploadExternalFiles(files);
    endDrag();
  }

  async function dropOnLocal(event: DragEvent) {
    event.preventDefault();
    localDropActive = false;
    const payload = parseDragPayload(event);
    if (payload?.side === "remote") await handleDownload(payload.names);
    endDrag();
  }

  async function handleDelete() {
    if (!sessionId || selectedRemoteFiles.length === 0) return;
    if (!confirm(`删除选中的 ${selectedRemoteFiles.length} 个远程项目?`)) return;
    try {
      for (const name of selectedRemoteFiles) await SFTPRemoveRecursive(sessionId, joinPath(remotePath, name));
      selectedRemote = null;
      selectedRemoteFiles = [];
      await loadRemote();
    } catch (e) { console.error("删除失败", e); }
  }

  async function handleMkdir() {
    if (!sessionId) return;
    const name = prompt("新建目录名称:")?.trim();
    if (!name || name.includes("/") || name === "." || name === "..") return;
    try {
      await SFTPMkdir(sessionId, joinPath(remotePath, name));
      await loadRemote();
    } catch (e) { console.error("创建目录失败", e); }
  }

  async function handleRename() {
    if (!sessionId || !selectedRemote) return;
    const name = prompt("新的名称:", selectedRemote)?.trim();
    if (!name || name === selectedRemote || name.includes("/")) return;
    try {
      await SFTPRename(sessionId, joinPath(remotePath, selectedRemote), joinPath(remotePath, name));
      selectedRemote = null;
      selectedRemoteFiles = [];
      await loadRemote();
    } catch (e) { console.error("重命名失败", e); }
  }

  async function handleChmod() {
    if (!sessionId || selectedRemoteFiles.length !== 1) return;
    const name = selectedRemoteFiles[0];
    const value = prompt("权限（八进制，例如 755）:", "644")?.trim();
    if (!value || !/^[0-7]{3,4}$/.test(value)) return;
    try {
      await SFTPChmod(sessionId, joinPath(remotePath, name), Number.parseInt(value, 8));
      await loadRemote();
    } catch (e) { remoteError = `修改权限失败: ${e}`; }
  }

  async function handleSymlink() {
    if (!sessionId) return;
    const target = prompt("目标路径:")?.trim();
    const name = prompt("链接名称:")?.trim();
    if (!target || !name || name.includes("/")) return;
    try {
      await SFTPSymlink(sessionId, target, joinPath(remotePath, name));
      await loadRemote();
    } catch (e) { remoteError = `创建软链接失败: ${e}`; }
  }

  async function openEditor() {
    if (!sessionId || !selectedRemote) return;
    const entry = remoteFiles.find((file) => file.name === selectedRemote);
    if (!entry || entry.isDir) return;
    editorPath = joinPath(remotePath, selectedRemote);
    editorBusy = true;
    editorError = "";
    try {
      editorContent = await SFTPReadFile(sessionId, editorPath);
      editorOpen = true;
    } catch (e: any) {
      editorError = e?.toString?.() || "读取文件失败";
    } finally {
      editorBusy = false;
    }
  }

  async function saveEditor() {
    if (!sessionId || !editorPath) return;
    editorBusy = true;
    editorError = "";
    try {
      await SFTPWriteFile(sessionId, editorPath, editorContent);
      editorOpen = false;
      await loadRemote();
    } catch (e: any) {
      editorError = e?.toString?.() || "保存文件失败";
    } finally {
      editorBusy = false;
    }
  }

  async function cancelTransfer() {
    if (!transfer || transfer.status !== "running") return;
    try { await SFTPCancelTransfer(transfer.id); } catch (e) { console.warn("取消传输失败", e); }
  }

  async function refreshDirectories() {
    if (!localLoading) await loadLocal(localPath);
    if (sessionId && !remoteLoading) await loadRemote(remotePath);
  }

  function restartAutoRefresh() {
    if (autoRefreshTimer) clearInterval(autoRefreshTimer);
    autoRefreshTimer = null;
    if (mounted && autoRefreshEnabled) {
      autoRefreshTimer = setInterval(() => void refreshDirectories(), Math.max(2, autoRefreshSeconds) * 1000);
    }
  }

  $effect(() => {
    const enabled = autoRefreshEnabled;
    const seconds = autoRefreshSeconds;
    if (typeof window !== "undefined") {
      localStorage.setItem("gossh.sftp.autoRefresh", JSON.stringify({ enabled, seconds }));
    }
    if (mounted) restartAutoRefresh();
  });

  onMount(() => {
    const unsubscribe = EventsOn("sftp:progress", (data: any) => {
      if (!data || !data.id) return;
      transfer = data;
      updateTransferQueue(data);
      if (data.status === "completed" || data.status === "failed" || data.status === "cancelled") {
        if (data.status === "completed") void refreshDirectories();
        setTimeout(() => {
          if (transfer?.id === data.id) transfer = null;
        }, 1600);
      }
    });
    try {
      const saved = JSON.parse(localStorage.getItem("gossh.sftp.autoRefresh") || "null");
      if (saved && typeof saved === "object") {
        autoRefreshEnabled = saved.enabled !== false;
        autoRefreshSeconds = Math.max(2, Math.min(60, Number(saved.seconds) || 5));
      }
    } catch {}
    mounted = true;
    restartAutoRefresh();
    void (async () => {
      try { localPath = await SFTPLocalHomeDir(); } catch { localPath = ""; }
      await loadLocal(localPath);
      await loadRemote(remotePath);
    })();
    return () => {
      mounted = false;
      unsubscribe();
      if (autoRefreshTimer) clearInterval(autoRefreshTimer);
      autoRefreshTimer = null;
    };
  });

  $effect(() => {
    const activeSession = sessionId;
    remoteLoadVersion++;
    selectedRemote = null;
    selectedRemoteFiles = [];
    remotePath = "/";
    remoteError = "";
    if (activeSession) void loadRemote("/");
    else remoteFiles = [];
  });
</script>

<div class="sftp-root">
  <div class="sftp-panels">
    <!-- 本地 -->
    <div class="sftp-panel">
      <div class="panel-header">
        <HardDrive class="w-3.5 h-3.5 text-blue-400" />
        <span class="panel-label">本地</span>
        <button class="pbtn" onclick={() => void loadLocal()}><RefreshCw class="w-3 h-3" /></button>
      </div>
      <div class="panel-path">
        <button class="up-btn" disabled={localLoading} onclick={goLocalUp}><ChevronRight class="w-3 h-3 rotate-180" /></button>
        <Home class="w-3 h-3 op-40" /><span class="p-text">{localPath || '(click refresh)'}</span>
      </div>
      <div class:drop-active={localDropActive} class="panel-list" role="listbox" aria-label="本地文件列表" tabindex="0"
           ondragover={(e) => handleDragOver("local", e)}
           ondragleave={(e) => handleDragLeave("local", e)}
           ondrop={dropOnLocal}>
        {#if localError}<div class="directory-error">{localError}</div>{/if}
        {#if localLoading}<div class="empty">正在读取目录...</div>{/if}
        {#each localFiles as f}
          <div class="frow {selectedLocalFiles.includes(f.name) ? 'sel' : ''}" role="button" tabindex="0"
               draggable={!f.isDir}
               onclick={(e) => f.isDir ? enterLocal(f) : selectFile('local', f.name, e)}
               ondragstart={(e) => !f.isDir && startDrag("local", f.name, e)}
               ondragend={endDrag}
               onkeydown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); f.isDir ? enterLocal(f) : selectFile('local', f.name, e as unknown as MouseEvent); } }}>
            {#if f.isDir}<Folder class="w-3.5 h-3.5 text-amber-400" />{:else}<File class="w-3.5 h-3.5 text-zinc-500" />{/if}
            <span class="fname">{f.name}</span>
            <span class="fsize">{formatSize(f.size)}</span>
          </div>
        {/each}
      </div>
    </div>
    <!-- 操作 -->
    <div class="sftp-actions">
      <button class="act-btn upload" disabled={selectedLocalFiles.length === 0 || !sessionId} onclick={() => void handleUpload()}>
        <Upload class="w-4 h-4" />{t("upload", $language)}
      </button>
      <button class="act-btn download" disabled={selectedRemoteFiles.length === 0 || !sessionId} onclick={() => void handleDownload()}>
        <Download class="w-4 h-4" />{t("download", $language)}
      </button>
      <button class="act-btn del" disabled={selectedRemoteFiles.length === 0 || !sessionId} onclick={handleDelete}>删除</button>
      <button class="act-btn" disabled={!sessionId} onclick={handleMkdir} title="新建目录">
        <FolderPlus class="w-4 h-4" />{t("directory", $language)}
      </button>
      <button class="act-btn" disabled={selectedRemoteFiles.length !== 1 || !sessionId} onclick={handleRename} title="重命名">
        <Pencil class="w-4 h-4" />{t("rename", $language)}
      </button>
      <button class="act-btn" disabled={selectedRemoteFiles.length !== 1 || !sessionId || editorBusy} onclick={openEditor} title="预览或编辑文件">
        <Eye class="w-4 h-4" />{t("preview", $language)}
      </button>
      <button class="act-btn" disabled={selectedRemoteFiles.length !== 1 || !sessionId} onclick={handleChmod} title="修改远程文件权限">{t("permissions", $language)}</button>
      <button class="act-btn" disabled={!sessionId} onclick={handleSymlink} title="创建远程软链接"><Link class="w-4 h-4" />链接</button>
      {#if transfer}
        <div class="transfer-status">
          <span>{transfer.type === 'upload' ? '上传' : '下载'} {transfer.status === 'failed' ? '失败' : transfer.status === 'cancelled' ? '已取消' : transfer.status === 'completed' ? '完成' : transfer.status === 'retrying' ? `重试 ${transfer.attempt}/3` : transfer.status === 'verifying' ? '校验中' : '中'}</span>
          {#if transfer.status === 'running'}<progress max="100" value={transfer.percent}></progress><span>{Math.round(transfer.percent)}%</span>{/if}
          {#if transfer.resumed}<span title="已从中断位置继续">续传</span>{/if}
          {#if transfer.verified}<span title="SHA-256 校验通过">已校验</span>{/if}
          {#if transfer.status === 'running'}<button class="cancel-transfer" title="取消传输" onclick={cancelTransfer}><X class="w-3 h-3" /></button>{/if}
        </div>
      {/if}
      {#if transferQueue.length}
        <div class="transfer-queue">
          <div class="transfer-queue-title">队列</div>
          {#each transferQueue as item}
            <div class="transfer-queue-row">
              <span class="transfer-name" title={item.fileName}>{item.type === "upload" ? "↑" : "↓"} {item.fileName}</span>
              <span class="transfer-state">{statusLabel(item)}</span>
              {#if item.status === "running" || item.status === "retrying" || item.status === "verifying"}
                <progress max="100" value={item.percent}></progress>
              {/if}
            </div>
          {/each}
        </div>
      {/if}
    </div>
    <!-- 远程 -->
    <div class="sftp-panel">
      <div class="panel-header">
        <Server class="w-3.5 h-3.5 text-emerald-400" />
        <span class="panel-label">远程</span>
        <button class="pbtn" disabled={!sessionId} title="OpenSSH 扩展与磁盘信息" onclick={loadRemoteInfo}><HardDrive class="w-3 h-3" /></button>
        <button class="pbtn" onclick={() => void loadRemote()}><RefreshCw class="w-3 h-3" /></button>
        <label class="refresh-toggle" title="自动刷新远程和本地目录"><input type="checkbox" bind:checked={autoRefreshEnabled} />自动</label>
        <select class="refresh-select" bind:value={autoRefreshSeconds} title="自动刷新间隔">
          <option value={5}>5秒</option><option value={10}>10秒</option><option value={30}>30秒</option><option value={60}>60秒</option>
        </select>
      </div>
      <div class="panel-path">
        <button class="up-btn" disabled={remoteLoading || remotePath === '/'} onclick={goRemoteUp}><ChevronRight class="w-3 h-3 rotate-180" /></button>
        <Home class="w-3 h-3 op-40" /><span class="p-text">{remotePath || '/'}</span>
      </div>
      <div class:drop-active={remoteDropActive} class="panel-list" role="listbox" aria-label="远程文件列表" tabindex="0"
           ondragover={(e) => handleDragOver("remote", e)}
           ondragleave={(e) => handleDragLeave("remote", e)}
           ondrop={dropOnRemote}>
        {#if remoteError}<div class="directory-error">{remoteError}</div>
        {:else if !sessionId}<div class="empty">请先连接 SSH</div>
        {:else if remoteLoading}<div class="empty">正在读取目录...</div>
        {:else if remoteFiles.length === 0}<div class="empty">空目录</div>{/if}
        {#each remoteFiles as f}
          <div class="frow {selectedRemoteFiles.includes(f.name) ? 'sel' : ''}" role="button" tabindex="0"
               draggable={!f.isDir}
               onclick={(e) => f.isDir ? enterRemote(f) : selectFile('remote', f.name, e)}
               ondragstart={(e) => !f.isDir && startDrag("remote", f.name, e)}
               ondragend={endDrag}
               onkeydown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); f.isDir ? enterRemote(f) : selectFile('remote', f.name, e as unknown as MouseEvent); } }}>
            {#if f.isDir}<Folder class="w-3.5 h-3.5 text-amber-400" />{:else}<File class="w-3.5 h-3.5 text-zinc-500" />{/if}
            <span class="fname">{f.name}</span>
            <span class="fsize" title={f.isSymlink ? f.linkTarget : f.perm}>{f.isSymlink ? `→ ${f.linkTarget || '链接'}` : f.perm}</span>
            <span class="fsize">{formatSize(f.size)}</span>
          </div>
        {/each}
      </div>
    </div>
  </div>
  {#if remoteInfoOpen}
    <div class="remote-info">
      <div class="remote-info-head">
        <span>OpenSSH SFTP</span>
        <button class="editor-icon" title="关闭" onclick={() => remoteInfoOpen = false}><X class="w-3.5 h-3.5" /></button>
      </div>
      {#if remoteInfoError}
        <div class="editor-error">{remoteInfoError}</div>
      {:else if remoteInfo}
        <div class="remote-info-row"><span>路径</span><strong>{remoteInfo.realPath || remotePath}</strong></div>
        <div class="remote-info-row"><span>posix-rename</span><strong>{remoteInfo.extensions?.posixRename ? "支持" : "不支持"}</strong></div>
        <div class="remote-info-row"><span>statvfs</span><strong>{remoteInfo.extensions?.statVfs ? "支持" : "不支持"}</strong></div>
        <div class="remote-info-row"><span>fsync</span><strong>{remoteInfo.extensions?.fsync ? "支持" : "不支持"}</strong></div>
        <div class="remote-info-row"><span>总空间</span><strong>{remoteInfo.usage ? formatSize(remoteInfo.usage.totalBytes || 0) : "不可用"}</strong></div>
        <div class="remote-info-row"><span>可用空间</span><strong>{remoteInfo.usage ? formatSize(remoteInfo.usage.availableBytes || 0) : "不可用"}</strong></div>
      {/if}
    </div>
  {/if}
  {#if editorOpen}
    <div class="editor-overlay" role="presentation" onclick={(e) => { if (e.target === e.currentTarget) editorOpen = false; }}>
      <div class="editor-modal" role="dialog" aria-modal="true" aria-label="编辑远程文件">
        <header class="editor-header"><span>{editorPath}</span><button class="editor-icon" title="关闭" onclick={() => editorOpen = false}><X class="w-4 h-4" /></button></header>
        <textarea class="editor-textarea" bind:value={editorContent} spellcheck="false" disabled={editorBusy}></textarea>
        {#if editorError}<div class="editor-error">{editorError}</div>{/if}
        <footer class="editor-footer"><button class="editor-cancel" onclick={() => editorOpen = false}>取消</button><button class="editor-save" disabled={editorBusy} onclick={saveEditor}><Save class="w-3.5 h-3.5" />保存</button></footer>
      </div>
    </div>
  {/if}
</div>

<style>
  .sftp-root { height: 100%; overflow: hidden; color: var(--app-text); }
  .sftp-panels { display: flex; height: 100%; }
  .sftp-panel {
    flex: 1; display: flex; flex-direction: column; overflow: hidden;
    background: var(--app-panel);
  }
  .sftp-actions {
    position: relative; width: 86px; display: flex; flex-direction: column; align-items: center;
    justify-content: center; gap: 6px; flex-shrink: 0;
    border-left: 1px solid var(--app-border);
    border-right: 1px solid var(--app-border);
  }
  .panel-header {
    display: flex; align-items: center; gap: 8px;
    padding: 6px 10px; border-bottom: 1px solid var(--app-border);
  }
  .panel-label { font-size: 12px; font-weight: 600; color: var(--app-text); }
  .pbtn { margin-left: auto; padding: 4px; border-radius: 4px; border: none; background: none; color: var(--app-subtle); cursor: pointer; }
  .pbtn + .pbtn { margin-left: 0; }
  .pbtn:hover { color: var(--app-text); background: var(--app-hover); }
  .refresh-toggle { display: inline-flex; align-items: center; gap: 3px; color: var(--app-subtle); font-size: 10px; white-space: nowrap; }
  .refresh-toggle input { accent-color: var(--app-accent); }
  .refresh-select { max-width: 50px; padding: 3px 2px; border: 1px solid var(--app-border); border-radius: 4px; background: var(--app-panel); color: var(--app-muted); font-size: 10px; }
  .panel-path {
    display: flex; align-items: center; gap: 4px; padding: 4px 10px;
    border-bottom: 1px solid var(--app-border); font-size: 10px;
    color: var(--app-muted); font-family: monospace; overflow: hidden;
  }
  .up-btn { background: none; border: none; cursor: pointer; color: var(--app-muted); display: flex; padding: 0; }
  .up-btn:disabled { opacity: 0.35; cursor: not-allowed; }
  :global(.op-40) { opacity: 0.4; }
  .p-text { flex: 1; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .panel-list { flex: 1; overflow-y: auto; outline: 1px solid transparent; outline-offset: -2px; transition: outline-color 0.12s, background 0.12s; }
  .panel-list.drop-active { outline-color: rgba(96,165,250,0.75); background: rgba(59,130,246,0.08); }
  .empty { text-align: center; padding: 20px; font-size: 11px; color: var(--app-subtle); }
  .directory-error { padding: 8px 10px; color: #fca5a5; background: rgba(239,68,68,0.1); font-size: 11px; overflow-wrap: anywhere; }
  .frow {
    display: flex; align-items: center; gap: 6px; padding: 5px 10px;
    font-size: 11px; color: var(--app-text); cursor: pointer; transition: background 0.1s;
    border-bottom: 1px solid var(--app-border);
  }
  .frow:hover { background: var(--app-hover); }
  .frow.sel { background: var(--app-accent-soft); border-color: rgba(99,102,241,0.2); }
  .fname { flex: 1; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .fsize { font-size: 10px; color: var(--app-subtle); font-family: monospace; flex-shrink: 0; }
  .act-btn {
    writing-mode: horizontal-tb; flex-direction: row; justify-content: center;
    width: 74px; height: 32px; padding: 6px 8px; border-radius: 6px;
    border: 1px solid var(--app-border); background: transparent;
    color: var(--app-muted); font-size: 11px; cursor: pointer; transition: all 0.15s;
    display: flex; align-items: center; gap: 4px; white-space: nowrap;
  }
  .act-btn:disabled { opacity: 0.3; cursor: not-allowed; }
  .act-btn.upload:not(:disabled):hover { background: rgba(59,130,246,0.15); color: #60a5fa; }
  .act-btn.download:not(:disabled):hover { background: rgba(16,185,129,0.15); color: #34d399; }
  .act-btn.del:not(:disabled):hover { background: rgba(239,68,68,0.15); color: #fca5a5; }
  .transfer-status { position: absolute; bottom: 8px; left: 50%; transform: translateX(-50%); display: flex; align-items: center; gap: 5px; width: 190px; padding: 6px 8px; border-radius: 6px; background: var(--app-panel-strong); border: 1px solid var(--app-border-strong); color: var(--app-text); font-size: 10px; z-index: 2; }
  .transfer-status progress { width: 70px; height: 5px; accent-color: #6366f1; }
  .cancel-transfer { display: inline-flex; padding: 2px; border: none; background: transparent; color: #fca5a5; cursor: pointer; }
  .transfer-queue {
    position: absolute; top: 8px; left: 50%; transform: translateX(-50%);
    width: 220px; max-height: 180px; overflow-y: auto; padding: 7px;
    border: 1px solid var(--app-border-strong); border-radius: 8px;
    background: var(--app-panel-strong); color: var(--app-text); z-index: 3;
    box-shadow: var(--app-shadow);
  }
  .transfer-queue-title { margin-bottom: 5px; color: var(--app-muted); font-size: 10px; font-weight: 600; }
  .transfer-queue-row { display: grid; grid-template-columns: 1fr auto; align-items: center; gap: 6px; padding: 4px 0; border-top: 1px solid var(--app-border); font-size: 10px; }
  .transfer-queue-row progress { grid-column: 1 / -1; width: 100%; height: 4px; accent-color: #6366f1; }
  .transfer-name { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .transfer-state { color: var(--app-muted); white-space: nowrap; }
  .remote-info {
    position: absolute; right: 14px; bottom: 14px; width: min(320px, calc(100% - 28px));
    padding: 10px; border-radius: 8px; border: 1px solid var(--app-border-strong);
    background: var(--app-panel-strong); box-shadow: var(--app-shadow); color: var(--app-muted); z-index: 3;
  }
  .remote-info-head { display: flex; align-items: center; justify-content: space-between; color: var(--app-text); font-size: 12px; font-weight: 600; margin-bottom: 8px; }
  .remote-info-row { display: flex; justify-content: space-between; gap: 10px; padding: 5px 0; border-top: 1px solid var(--app-border); font-size: 11px; }
  .remote-info-row strong { color: var(--app-text); font-weight: 500; text-align: right; overflow-wrap: anywhere; }
  .editor-overlay { position: absolute; inset: 0; z-index: 20; display: flex; align-items: center; justify-content: center; background: rgba(2,6,23,0.72); }
  .editor-modal { display: flex; flex-direction: column; width: min(760px, 90%); height: min(620px, 88%); background: var(--app-panel-strong); border: 1px solid var(--app-border-strong); border-radius: 10px; box-shadow: var(--app-shadow); overflow: hidden; }
  .editor-header, .editor-footer { display: flex; align-items: center; gap: 8px; padding: 9px 12px; border-bottom: 1px solid var(--app-border); color: var(--app-text); font-size: 11px; }
  .editor-header span { flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-family: monospace; }
  .editor-icon { display: inline-flex; padding: 4px; border: none; background: transparent; color: var(--app-muted); cursor: pointer; }
  .editor-textarea { flex: 1; resize: none; padding: 12px; border: none; outline: none; background: var(--app-bg-soft); color: var(--app-text); font: 12px/1.55 monospace; }
  .editor-error { padding: 6px 12px; color: #fca5a5; background: rgba(239,68,68,0.1); font-size: 11px; }
  .editor-footer { justify-content: flex-end; border-top: 1px solid var(--app-border); border-bottom: none; }
  .editor-cancel, .editor-save { display: inline-flex; align-items: center; gap: 5px; padding: 6px 10px; border-radius: 5px; border: 1px solid var(--app-border-strong); background: transparent; color: var(--app-text); font-size: 11px; cursor: pointer; }
  .editor-save { background: #4f46e5; border-color: #6366f1; color: white; }
  .editor-save:disabled { opacity: 0.45; cursor: not-allowed; }
</style>
