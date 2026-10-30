"use client";

import { Suspense, useState } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { userAdmin, ApiError } from "@/lib/api";
import { AuthShell } from "@/components/AuthShell";

function ResetPasswordInner() {
  const params = useSearchParams();
  const [token, setToken] = useState(params.get("token") || "");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [done, setDone] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError("");
    if (password !== confirm) {
      setError("Passwords do not match");
      return;
    }
    setBusy(true);
    try {
      await userAdmin.resetPassword({ token, password });
      setDone(true);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Could not reset password");
    } finally {
      setBusy(false);
    }
  }

  if (done) {
    return (
      <AuthShell title="Password updated">
        <p className="mb-4 text-center text-sm text-slate-500">You can now sign in with your new password.</p>
        <Link href="/login" className="btn-primary w-full justify-center">
          Go to sign in
        </Link>
      </AuthShell>
    );
  }

  return (
    <AuthShell title="Set a new password">
      <form onSubmit={submit} className="space-y-4">
        <div>
          <label className="label">Reset token</label>
          <input className="input" value={token} onChange={(e) => setToken(e.target.value)} required />
        </div>
        <div>
          <label className="label">New password</label>
          <input
            className="input"
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            minLength={8}
            required
          />
          <p className="mt-1 text-xs text-slate-400">At least 8 characters.</p>
        </div>
        <div>
          <label className="label">Confirm password</label>
          <input
            className="input"
            type="password"
            value={confirm}
            onChange={(e) => setConfirm(e.target.value)}
            minLength={8}
            required
          />
        </div>
        {error && <p className="text-sm text-red-600">{error}</p>}
        <button className="btn-primary w-full" disabled={busy}>
          {busy ? "Saving…" : "Set password"}
        </button>
      </form>
    </AuthShell>
  );
}

export default function ResetPasswordPage() {
  return (
    <Suspense fallback={<div className="p-8 text-slate-400">Loading…</div>}>
      <ResetPasswordInner />
    </Suspense>
  );
}
