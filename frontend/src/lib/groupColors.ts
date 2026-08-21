// owner: muswood | Email: mumu920@outlook.com
const palette = [
  "#60a5fa", "#34d399", "#fbbf24", "#f472b6", "#a78bfa",
  "#fb7185", "#22d3ee", "#c084fc", "#84cc16", "#f97316",
];

const storageKey = "gossh.group.colors";
let assignments: Record<string, string> = {};

if (typeof localStorage !== "undefined") {
  try {
    assignments = JSON.parse(localStorage.getItem(storageKey) || "{}") as Record<string, string>;
  } catch {
    assignments = {};
  }
}

function hash(value: string): number {
  let result = 0;
  for (let i = 0; i < value.length; i++) result = ((result << 5) - result + value.charCodeAt(i)) | 0;
  return Math.abs(result);
}

function persist() {
  if (typeof localStorage === "undefined") return;
  try { localStorage.setItem(storageKey, JSON.stringify(assignments)); } catch { /* storage may be unavailable */ }
}

export function getGroupColor(groupId: string): string {
  const id = groupId || "ungrouped";
  if (assignments[id]) return assignments[id];
  const used = new Set(Object.values(assignments));
  const start = hash(id) % palette.length;
  let selected = palette[start];
  for (let offset = 0; offset < palette.length; offset++) {
    const candidate = palette[(start + offset) % palette.length];
    if (!used.has(candidate)) {
      selected = candidate;
      break;
    }
  }
  assignments[id] = selected;
  persist();
  return selected;
}

export function setGroupColor(groupId: string, color: string) {
  if (!/^#[0-9a-f]{6}$/i.test(color)) return;
  assignments[groupId || "ungrouped"] = color;
  persist();
}
