// owner: muswood | Email: mumu920@outlook.com
import { SSHConnectByID, SSHConnectByIDWithPassword, SSHTrustHostKey } from "../../wailsjs/go/main/App";
import { confirmDialog } from "$lib/dialogs";
import { requestPassword } from "$lib/passwordPrompt";

function fingerprintFromError(error: unknown): string | undefined {
  const match = String(error).match(/指纹:\s*(SHA256:[A-Za-z0-9+/=]+)/);
  return match?.[1];
}

export async function connectSSHWithHostTrust(connectionId: string, cols: number, rows: number): Promise<string> {
  let password: string | undefined;
  try {
    return await SSHConnectByID(connectionId, cols, rows);
  } catch (error) {
    if (/密码不能为空/.test(String(error))) {
      const input = await requestPassword("输入 SSH 密码", "此连接未保存密码，仅本次连接使用。");
      if (!input) throw error;
      password = input;
      return await SSHConnectByIDWithPassword(connectionId, password, cols, rows);
    }
    const fingerprint = fingerprintFromError(error);
    if (!fingerprint) throw error;
    const confirmed = await confirmDialog(
      "确认 SSH 主机密钥",
      `此服务器的 SSH 主机密钥尚未被信任。\n\nSHA-256 指纹：\n${fingerprint}\n\n请仅在已通过其他可信渠道核对该指纹后确认。确认后将写入 known_hosts。`,
      "信任并连接",
    );
    if (!confirmed) throw error;
    await SSHTrustHostKey(connectionId, fingerprint);
    return await SSHConnectByID(connectionId, cols, rows);
  }
}
