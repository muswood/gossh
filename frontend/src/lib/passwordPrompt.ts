// owner: muswood | Email: mumu920@outlook.com
import { writable } from "svelte/store";

type PasswordPrompt = {
  title: string;
  message: string;
  resolve: (password: string | null) => void;
};

export type KeyboardInteractivePrompt = {
  requestId: string;
  user: string;
  instruction: string;
  questions: string[];
  echos: boolean[];
  resolve: (answers: string[] | null) => void;
};

export const passwordPrompt = writable<PasswordPrompt | null>(null);
export const keyboardInteractivePrompt = writable<KeyboardInteractivePrompt | null>(null);

export function requestPassword(title: string, message: string): Promise<string | null> {
  return new Promise(resolve => passwordPrompt.set({ title, message, resolve }));
}

export function requestKeyboardInteractive(prompt: Omit<KeyboardInteractivePrompt, "resolve">): Promise<string[] | null> {
  return new Promise(resolve => keyboardInteractivePrompt.set({ ...prompt, resolve }));
}
