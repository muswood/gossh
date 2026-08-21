// owner: muswood | Email: mumu920@outlook.com
export type CommandBlock = {
  command: string;
  cwd: string;
  exitCode: number;
  output: string;
};

// OSC 133 (FinalTerm) and OSC 633 (VS Code shell integration) delimit commands.
// The parser keeps incomplete escape sequences between SSH read chunks.
export class ShellIntegrationParser {
  private pending = "";
  private command = "";
  private cwd = "";
  private output = "";
  private collecting = false;

  feed(chunk: string): CommandBlock[] {
    const blocks: CommandBlock[] = [];
    let input = this.pending + chunk;
    this.pending = "";
    let cursor = 0;

    while (cursor < input.length) {
      const start = input.indexOf("\u001b]", cursor);
      if (start < 0) {
        this.appendOutput(input.slice(cursor));
        break;
      }
      this.appendOutput(input.slice(cursor, start));
      const end = oscEnd(input, start + 2);
      if (!end) {
        this.pending = input.slice(start);
        break;
      }
      this.handleOSC(input.slice(start + 2, end.payloadEnd), blocks);
      cursor = end.next;
    }
    return blocks;
  }

  getCwd() { return this.cwd; }
  getCommand() { return this.command; }
  isActive() { return this.collecting; }

  private appendOutput(value: string) {
    if (!this.collecting || !value) return;
    this.output = trimOutput(this.output + stripTerminalControls(value));
  }

  private handleOSC(payload: string, blocks: CommandBlock[]) {
    const separator = payload.indexOf(";");
    if (separator < 0) return;
    const code = payload.slice(0, separator);
    const data = payload.slice(separator + 1);
    if (code === "7") {
      this.setCwdFromURI(data);
      return;
    }
    if (code !== "133" && code !== "633") return;

    const [marker, ...parts] = data.split(";");
    const value = parts.join(";");
    switch (marker) {
      case "B":
        this.output = "";
        this.collecting = true;
        break;
      case "C":
        if (value) this.command = decodeShellValue(value);
        this.output = "";
        this.collecting = true;
        break;
      case "D":
        if (!this.collecting) return;
        const exitCode = Number.parseInt(parts[0] || "0", 10);
        blocks.push({
          command: this.command,
          cwd: this.cwd,
          exitCode: Number.isFinite(exitCode) ? exitCode : 0,
          output: this.output,
        });
        this.output = "";
        this.collecting = false;
        break;
      case "E":
        this.command = decodeShellValue(value);
        break;
      case "P": {
        const equals = value.indexOf("=");
        if (equals > 0 && value.slice(0, equals).toLowerCase() === "cwd") {
          this.cwd = decodeShellValue(value.slice(equals + 1));
        }
        break;
      }
    }
  }

  private setCwdFromURI(uri: string) {
    try {
      const parsed = new URL(uri);
      if (parsed.protocol === "file:") this.cwd = decodeURIComponent(parsed.pathname);
    } catch {
      // OSC 7 is optional metadata and must never affect terminal output.
    }
  }
}

function oscEnd(input: string, start: number) {
  for (let i = start; i < input.length; i++) {
    if (input[i] === "\u0007") return { payloadEnd: i, next: i + 1 };
    if (input[i] === "\u001b" && input[i + 1] === "\\") return { payloadEnd: i, next: i + 2 };
  }
  return undefined;
}

function decodeShellValue(value: string) {
  try { return decodeURIComponent(value.replace(/\\x([0-9a-f]{2})/gi, "%$1")); } catch { return value; }
}

function stripTerminalControls(value: string) {
  return value
    .replace(/\u001b\[[0-?]*[ -/]*[@-~]/g, "")
    .replace(/\r(?!\n)/g, "\n")
    .replace(/[\u0000-\u0008\u000b-\u001a\u001c-\u001f\u007f]/g, "");
}

function trimOutput(value: string) {
  return value.length > 12000 ? value.slice(-12000) : value;
}
