import { MouseEvent } from "react";
import { api } from "../api";

const promptUnlockAfterSignInKey = "mariner_prompt_unlock_after_sign_in";

export function consumePromptUnlockAfterSignIn() {
  const shouldPrompt = sessionStorage.getItem(promptUnlockAfterSignInKey) === "true";
  if (shouldPrompt) sessionStorage.removeItem(promptUnlockAfterSignInKey);
  return shouldPrompt;
}

export function signOut(event?: MouseEvent<HTMLAnchorElement>) {
  event?.preventDefault();
  sessionStorage.setItem(promptUnlockAfterSignInKey, "true");
  void api.logout().finally(() => window.location.assign("/"));
}

export function promptUnlockOnReturn() {
  sessionStorage.setItem(promptUnlockAfterSignInKey, "true");
}
