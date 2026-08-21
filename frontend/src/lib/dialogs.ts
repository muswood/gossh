// owner: muswood | Email: mumu920@outlook.com
import { writable } from "svelte/store";

export type AppDialog = {
  title: string;
  message: string;
  confirmLabel?: string;
  resolve?: (confirmed: boolean) => void;
};

export const appDialog = writable<AppDialog | null>(null);

export function showErrorDialog(title: string, message: string) {
  appDialog.set({ title, message });
}

export function confirmDialog(title: string, message: string, confirmLabel: string): Promise<boolean> {
  return new Promise(resolve => {
    appDialog.set({ title, message, confirmLabel, resolve });
  });
}
