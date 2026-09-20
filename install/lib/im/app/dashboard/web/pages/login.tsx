import { useState } from "react";
import type { FormEvent } from "react";

import { apiFetch, saveSession, type AdminSession } from "../api";
import { Button } from "../ui/button";
import { Field } from "../ui/field";
import { Input } from "../ui/inputs";

type LoginResponse = {
  token: string;
  expires_at: string;
  username: string;
};

export function Login(props: { onSignedIn: (session: AdminSession) => void }) {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (submitting) return;
    setError(null);
    setSubmitting(true);
    try {
      const session = await apiFetch<LoginResponse>("/login", {
        method: "POST",
        body: JSON.stringify({ username: username.trim(), password }),
      });
      const full: AdminSession = { ...session, username: session.username || username.trim() };
      saveSession(full);
      props.onSignedIn(full);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Sign-in failed");
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div class="admin-login-wrap">
      <form class="admin-login-card" onSubmit={submit}>
        <div class="admin-login-brand">IM Admin</div>
        <p class="admin-login-hint">
          Sign in with the console credentials configured through
          {" "}
          <code>IM_ADMIN_USERNAME</code> / <code>IM_ADMIN_PASSWORD</code>.
        </p>
        <Field label="Username" required>
          {(id) => (
            <Input
              id={id}
              value={username}
              autofocus
              autocomplete="username"
              placeholder="admin"
              invalid={!!error}
              onInput={(e) => setUsername((e.target as HTMLInputElement).value)}
            />
          )}
        </Field>
        <Field label="Password" required error={error ?? undefined}>
          {(id) => (
            <Input
              id={id}
              type="password"
              value={password}
              autocomplete="current-password"
              placeholder="••••••••"
              invalid={!!error}
              onInput={(e) => setPassword((e.target as HTMLInputElement).value)}
            />
          )}
        </Field>
        <Button type="submit" variant="primary" loading={submitting} class="admin-login-submit">
          Sign in
        </Button>
      </form>
    </div>
  );
}
