<!-- owner: muswood | Email: mumu920@outlook.com -->
<script lang="ts">
  import {
    ChevronRight, Folder, FolderOpen, Server, Search,
    Star, Edit3, Cpu, Trash2, FolderPlus
  } from "lucide-svelte";
  import { connectionGroups, tabs, activeTabId, connRefreshTrigger, language } from "$lib/stores";
  import { t } from "$lib/i18n";
  import type { Connection, ConnectionGroup } from "$lib/stores";
  import { getGroupColor, setGroupColor } from "$lib/groupColors";
  import { config } from "../../../wailsjs/go/models";
  import { onMount } from "svelte";
  import {
    ListConnections, ListGroups, SaveGroup,
    DeleteConnection, SaveConnection, DeleteGroup, TCPConnect, SetConnectionGroup
  } from "../../../wailsjs/go/main/App";
  import { connectSSHWithHostTrust } from "$lib/sshConnect";
  import { showErrorDialog } from "$lib/dialogs";

  let { onNewClick, onEditClick } = $props<{
    onNewClick?: () => void;
    onEditClick?: (connection: Connection) => void;
  }>();
  let searchQuery = $state("");
  let expandedGroups = $state(new Set<string>());
  let loaded = $state(false);
  let groupColorVersion = $state(0);
  let draggedConnectionId = $state<string | null>(null);

  onMount(async () => {
    await loadFromDB();
    loaded = true;
  });

  connRefreshTrigger.subscribe(async (v) => {
    if (v > 0) await loadFromDB();
  });

  async function loadFromDB() {
    if (!(window as any).go?.main?.App) {
      connectionGroups.set([]);
      expandedGroups = new Set();
      return;
    }
    try {
      const connsJson = await ListConnections();
      const groupsJson = await ListGroups();
      const conns = JSON.parse(connsJson || "[]");
      const groups = JSON.parse(groupsJson || "[]");

      const grouped: Record<string, Connection[]> = {};
      for (const c of conns) {
        const gid = c.groupId || c.group_id || "ungrouped";
        if (!grouped[gid]) grouped[gid] = [];
        grouped[gid].push({
          id: c.id, name: c.name, host: c.host, port: c.port || 22,
	          protocol: c.protocol || "ssh",
          username: c.username, groupId: gid, connected: false,
          starred: c.starred, authMethod: c.authMethod || c.auth_method || "password",
          password: c.password || "", jumpHost: c.jumpHost || c.jump_host || "",
          privateKeyPath: c.privateKeyPath || c.private_key_path || "",
          certificatePath: c.certificatePath || c.certificate_path || "",
          startupCmd: c.startupCmd || c.startup_cmd || "",
          encoding: c.encoding || "utf-8", keepAliveSeconds: c.keepAliveSeconds || c.keep_alive || 30,
          terminalTheme: c.terminalTheme || c.terminal_theme || "",
          serialBaudRate: c.serialBaudRate || 115200,
          serialDataBits: c.serialDataBits || 8,
          serialStopBits: c.serialStopBits || 1,
          serialParity: c.serialParity || "none",
          serialAutoReconnect: c.serialAutoReconnect !== false,
          groupColor: getGroupColor(gid),
        });
      }

      const result: ConnectionGroup[] = [];
      for (const g of groups) {
        result.push({
          id: g.id, name: g.name,
          items: grouped[g.id] || [],
        });
        delete grouped[g.id];
      }
      for (const [gid, items] of Object.entries(grouped)) {
        result.push({ id: gid, name: gid === "ungrouped" ? t("ungrouped", $language) : gid, items });
      }
      connectionGroups.set(result);
      // 默认展开所有分组
      expandedGroups = new Set(result.map(g => g.id));
    } catch (e) {
      console.warn("加载连接失败", e);
    }
  }

  async function connect(conn: Connection) {
    const protocol = conn.protocol || "ssh";
    const id = `${protocol}-${conn.id}-${Date.now()}`;
    const isSerial = protocol === "serial";
    const tab = {
      id, type: protocol as "ssh" | "telnet" | "raw" | "serial", name: conn.name, connected: isSerial, sessionId: isSerial ? undefined : id,
      connectionId: conn.id, terminalTheme: conn.terminalTheme || undefined,
      groupColor: conn.groupColor || getGroupColor(conn.groupId),
      serialConfig: isSerial ? { portName: conn.host, baudRate: conn.serialBaudRate || 115200, dataBits: conn.serialDataBits || 8, stopBits: conn.serialStopBits || 1, parity: conn.serialParity || "none", autoReconnect: conn.serialAutoReconnect !== false } : undefined,
    };
    tabs.update(t => [...t, tab]);
    activeTabId.set(id);
    try {
      if (isSerial) {
        tabs.update(t => t.map(x => x.id === id ? { ...x, connected: true } : x));
        return;
      }
      const sessionId = protocol === "ssh"
        ? await connectSSHWithHostTrust(conn.id, 80, 24)
        : await TCPConnect({ id, host: conn.host, port: conn.port, protocol });
      tabs.update(t => t.map(x => x.id === id ? { ...x, sessionId, connected: true } : x));
    } catch (e: any) {
      const msg = e?.toString ? e.toString() : String(e || "连接失败");
      tabs.update(t => t.filter(x => x.id !== id));
      activeTabId.set("welcome");
      showErrorDialog(`${protocol.toUpperCase()} 连接失败`, msg);
    }
  }

  async function removeConnection(conn: Connection) {
    try {
      await DeleteConnection(conn.id);
      await loadFromDB();
    } catch (e) {
      console.error(e);
    }
  }

  async function moveConnection(connectionID: string, groupID: string) {
    if (!connectionID || !groupID) return;
    try {
      await SetConnectionGroup(connectionID, groupID);
      await loadFromDB();
    } catch (e) {
      console.error("移动连接分组失败", e);
    } finally {
      draggedConnectionId = null;
    }
  }

  async function toggleStar(conn: Connection) {
    try {
      await SaveConnection(new config.ConnectionRecord({
        id: conn.id, name: conn.name, host: conn.host, port: conn.port,
	        protocol: conn.protocol || "ssh", username: conn.username, authMethod: conn.authMethod || "password",
        password: "", privateKey: "", privateKeyPath: conn.privateKeyPath || "", certificatePath: conn.certificatePath || "", passphrase: "", jumpHost: conn.jumpHost || "",
        encoding: conn.encoding || "utf-8", startupCmd: conn.startupCmd || "",
        keepAliveSeconds: conn.keepAliveSeconds || 30, groupId: conn.groupId,
        terminalTheme: conn.terminalTheme || "",
        serialBaudRate: conn.serialBaudRate || 115200,
        serialDataBits: conn.serialDataBits || 8,
        serialStopBits: conn.serialStopBits || 1,
        serialParity: conn.serialParity || "none",
        serialAutoReconnect: conn.serialAutoReconnect !== false,
        starred: !conn.starred,
      }));
      await loadFromDB();
    } catch (e) { console.error("更新收藏状态失败", e); }
  }

  async function createGroup() {
    const name = prompt("分组名称:")?.trim();
    if (!name) return;
    await SaveGroup({ id: `group-${Date.now()}`, name });
    await loadFromDB();
  }

  async function renameGroup(group: ConnectionGroup) {
    const name = prompt("新的分组名称:", group.name)?.trim();
    if (!name || name === group.name) return;
    await SaveGroup({ id: group.id, name });
    await loadFromDB();
  }

  async function removeGroup(group: ConnectionGroup) {
    if (group.id === "ungrouped") return;
    if (!confirm(`删除分组“${group.name}”？其中的连接会移到未分组。`)) return;
    await DeleteGroup(group.id);
    await loadFromDB();
  }

  function toggleGroup(id: string) {
    if (expandedGroups.has(id)) expandedGroups.delete(id);
    else expandedGroups.add(id);
    expandedGroups = new Set(expandedGroups);
  }

  function colorForGroup(id: string) {
    groupColorVersion;
    return getGroupColor(id);
  }

  function updateGroupColor(group: ConnectionGroup, color: string) {
    setGroupColor(group.id, color);
    const connectionIDs = new Set(group.items.map(item => item.id));
    connectionGroups.update(groups => groups.map(item => item.id === group.id
      ? { ...item, items: item.items.map(connection => ({ ...connection, groupColor: color })) }
      : item));
    tabs.update(items => items.map(tab => tab.type === "ssh" && tab.connectionId && connectionIDs.has(tab.connectionId)
      ? { ...tab, groupColor: color }
      : tab));
    groupColorVersion += 1;
  }

  let filted = $derived(
    !searchQuery.trim() ? $connectionGroups
      : $connectionGroups.map(g => ({
        ...g,
        items: g.items.filter(i =>
          i.name.toLowerCase().includes(searchQuery.toLowerCase()) ||
          i.host.toLowerCase().includes(searchQuery.toLowerCase())
        ),
      })).filter(g => g.items.length > 0)
  );
</script>

<div class="tree-root">
  <div class="tree-header">
    <label class="search-box">
      <Search class="w-3.5 h-3.5 search-icon" />
      <input type="text" placeholder={t("searchConnections", $language)} bind:value={searchQuery} />
    </label>
  </div>

  <div class="tree-list">
    {#if !loaded}
      <div class="tree-empty">加载中...</div>
    {:else if filted.length === 0}
      <div class="tree-empty">
        <p>{t("noConnections", $language)}</p>
        <p class="text-[11px] mt-1 opacity-50">点击下方按钮创建</p>
      </div>
    {:else}
      {#each filted as group (group.id)}
        <div class="tree-group" style={`--group-color: ${colorForGroup(group.id)}`}>
          <div class="group-header" role="button" tabindex="0"
               ondragover={(e) => { e.preventDefault(); (e.currentTarget as HTMLElement).classList.add('drop-target'); }}
               ondragleave={(e) => (e.currentTarget as HTMLElement).classList.remove('drop-target')}
               ondrop={(e) => { e.preventDefault(); (e.currentTarget as HTMLElement).classList.remove('drop-target'); if (draggedConnectionId) void moveConnection(draggedConnectionId, group.id); }}
               onclick={() => toggleGroup(group.id)}
               onkeydown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); toggleGroup(group.id); } }}>
            <ChevronRight class="w-3 h-3 chevron {expandedGroups.has(group.id) ? 'rotated' : ''}" />
            {#if expandedGroups.has(group.id)}
              <FolderOpen class="w-3.5 h-3.5" style={`color: ${colorForGroup(group.id)}`} />
            {:else}
              <Folder class="w-3.5 h-3.5" style={`color: ${colorForGroup(group.id)}`} />
            {/if}
            <span class="group-name" style={`color: ${colorForGroup(group.id)}`}>{group.name}</span>
            <span class="group-count">{group.items.length}</span>
            {#if group.id !== 'ungrouped'}
              <span class="group-actions">
                <input class="group-color-picker" type="color" value={colorForGroup(group.id)} title="设置分组颜色" aria-label={`设置 ${group.name} 的颜色`}
                       onclick={(e) => e.stopPropagation()} onchange={(e) => updateGroupColor(group, (e.currentTarget as HTMLInputElement).value)} />
                <button class="conn-act" title="重命名分组" onclick={(e) => { e.stopPropagation(); renameGroup(group); }}><Edit3 class="w-3 h-3" /></button>
                <button class="conn-act del" title="删除分组" onclick={(e) => { e.stopPropagation(); removeGroup(group); }}><Trash2 class="w-3 h-3" /></button>
              </span>
            {/if}
          </div>

          {#if expandedGroups.has(group.id)}
            <div class="group-items">
              {#each group.items as item (item.id)}
                <div class="conn-item" role="button" tabindex="0" draggable="true"
                     ondragstart={(e) => { draggedConnectionId = item.id; e.dataTransfer?.setData('text/plain', item.id); if (e.dataTransfer) e.dataTransfer.effectAllowed = 'move'; }}
                     ondragend={() => { draggedConnectionId = null; }}
                     style={`--group-color: ${item.groupColor || colorForGroup(group.id)}`}
                     onclick={() => connect(item)}
                     onkeydown={(e) => (e.key === 'Enter') && connect(item)}>
                  <div class="conn-icon">
                    <Server class="w-3 h-3" style={`color: ${item.groupColor || colorForGroup(group.id)}`} />
                    {#if item.connected}
                      <span class="conn-dot"></span>
                    {/if}
                  </div>
                  <div class="conn-text">
                    <div class="conn-name" style={`color: ${item.groupColor || colorForGroup(group.id)}`}>{item.name}</div>
                    <div class="conn-addr">{item.username}@{item.host}</div>
                  </div>
                  <div class="conn-actions">
                    <button class="conn-act star {item.starred ? 'active' : ''}" onclick={(e) => { e.stopPropagation(); toggleStar(item); }} title={item.starred ? '取消收藏' : '收藏'}>
                      <Star class="w-3 h-3" />
                    </button>
                    <button class="conn-act" onclick={(e) => { e.stopPropagation(); onEditClick?.(item); }} title="编辑">
                      <Edit3 class="w-3 h-3" />
                    </button>
                    <button class="conn-act del" onclick={(e) => { e.stopPropagation(); removeConnection(item); }} title="删除">
                      <Trash2 class="w-3 h-3" />
                    </button>
                  </div>
                </div>
              {/each}
            </div>
          {/if}
        </div>
      {/each}
    {/if}
  </div>

  <div class="tree-footer">
    <button class="group-btn" onclick={createGroup} title="新建分组">
      <FolderPlus class="w-3.5 h-3.5" /> {t("newGroup", $language)}
    </button>
    <button class="new-btn" onclick={() => onNewClick?.()}>
      <Server class="w-3.5 h-3.5" /> {t("newConnection", $language)}
    </button>
  </div>
</div>

<style>
  .tree-root { display: flex; flex-direction: column; height: 100%; border-right: 1px solid var(--app-border); background: var(--app-panel); }
  .tree-header { padding: 12px; border-bottom: 1px solid var(--app-border); }
  .search-box {
    display: flex; align-items: center; gap: 8px;
    background: var(--app-panel-muted); border: 1px solid var(--app-border);
    border-radius: 8px; padding: 7px 10px;
  }
  :global(.search-icon) { color: var(--app-subtle); flex-shrink: 0; }
  .search-box input {
    background: transparent; border: none; outline: none; color: var(--app-text);
    font-size: 12px; width: 100%;
  }
  .search-box input::placeholder { color: var(--app-subtle); }
  .tree-list { flex: 1; overflow-y: auto; padding: 8px; }
  .tree-empty { text-align: center; padding: 24px 8px; font-size: 12px; color: var(--app-subtle); }
  .group-header {
    display: flex; align-items: center; gap: 6px; width: 100%;
    padding: 6px 8px; border-radius: 6px; border: none; background: transparent;
    cursor: pointer; font-size: 11px; font-weight: 600; color: var(--app-muted);
    transition: all 0.1s;
  }
  .group-header:hover { background: color-mix(in srgb, var(--group-color) 10%, transparent); color: var(--app-text); }
  :global(.group-header.drop-target) { outline: 1px solid var(--group-color); background: color-mix(in srgb, var(--group-color) 18%, transparent); }
  .group-name { flex: 1; text-align: left; }
  .group-count { font-size: 10px; opacity: 0.4; }
  .group-actions { display: flex; gap: 2px; opacity: 0; }
  .group-header:hover .group-actions { opacity: 1; }
  .group-color-picker { width: 17px; height: 17px; padding: 0; border: 0; border-radius: 4px; background: transparent; cursor: pointer; }
  .group-color-picker::-webkit-color-swatch-wrapper { padding: 0; }
  .group-color-picker::-webkit-color-swatch { border: 1px solid var(--app-border-strong); border-radius: 4px; }
  :global(.chevron) { transition: transform 0.15s; }
  :global(.chevron.rotated) { transform: rotate(90deg); }
  .group-items { margin-left: 14px; padding-left: 8px; border-left: 2px solid color-mix(in srgb, var(--group-color) 45%, transparent); }
  .conn-item {
    display: flex; align-items: center; gap: 8px;
    padding: 6px 8px; border-radius: 6px; cursor: pointer;
    transition: all 0.1s; font-size: 11px;
  }
  .conn-item:hover { background: var(--app-accent-soft); }
  .conn-icon { position: relative; width: 22px; height: 22px; border-radius: 6px; background: color-mix(in srgb, var(--group-color) 14%, transparent); border: 1px solid color-mix(in srgb, var(--group-color) 32%, transparent); display: flex; align-items: center; justify-content: center; color: var(--group-color); flex-shrink: 0; }
  .conn-dot { position: absolute; top: -2px; right: -2px; width: 6px; height: 6px; border-radius: 50%; background: #4ade80; box-shadow: 0 0 5px rgba(74, 222, 128, 0.5); }
  .conn-text { flex: 1; min-width: 0; }
  .conn-name { font-weight: 500; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .conn-addr { color: var(--app-muted); font-size: 10px; font-family: monospace; }
  .conn-actions { opacity: 0; transition: opacity 0.1s; }
  .conn-item:hover .conn-actions { opacity: 1; }
  .conn-act { background: none; border: none; padding: 2px; border-radius: 3px; cursor: pointer; color: var(--app-subtle); display: flex; }
  .conn-act.del:hover { background: rgba(239, 68, 68, 0.15); color: #fca5a5; }
  .conn-act.star.active { color: #fbbf24; opacity: 1; }
  .tree-footer { display: flex; gap: 6px; padding: 12px; border-top: 1px solid var(--app-border); }
  .group-btn {
    display: flex; align-items: center; justify-content: center; gap: 5px;
    padding: 8px 7px; border-radius: 8px; border: 1px solid var(--app-border);
    background: transparent; color: var(--app-muted); font-size: 11px; cursor: pointer;
  }
  .group-btn:hover { color: var(--app-text); background: var(--app-hover); }
  .new-btn {
    display: flex; align-items: center; justify-content: center; gap: 8px;
    flex: 1; padding: 8px; border-radius: 8px; border: 1px solid rgba(99, 102, 241, 0.2);
    background: var(--app-accent-soft); color: var(--app-accent);
    font-size: 12px; font-weight: 500; cursor: pointer; transition: all 0.15s;
  }
  .new-btn:hover { background: rgba(99, 102, 241, 0.25); box-shadow: 0 0 16px rgba(99, 102, 241, 0.1); }
</style>
