"use client";

import { Suspense, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { api, ApiError } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { AuthShell } from "@/components/AuthShell";

function AcceptInviteInner() {
  const { apply } = useAuth();
  const router = useRouter();
  const params = useSearchParams();
  const [token, setToken] = useState(params.get("token") || "");
  const [fullName, setFullName] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError("");
    setBusy(true);
    try {
      const p = await api.acceptInvite({ token, full_name: fullName, password: password || undefined });
      apply(p);
      router.replace("/dashboard");
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Could not accept invite");
    } finally {
      setBusy(false);
    }
  }

  return (
    <AuthShell title="Accept your invitation">
      <form onSubmit={submit} className="space-y-4">
        <div>
          <label className="label">Invitation token</label>
          <input className="input" value={token} onChange={(e) => setToken(e.target.value)} required />
        </div>
        <div>
          <label className="label">Your name</label>
          <input className="input" value={fullName} onChange={(e) => setFullName(e.target.value)} />
        </div>
        <div>
          <label className="label">Set a password</label>
          <input className="input" type="password" value={password} onChange={(e) => setPassword(e.target.value)} minLength={8} />
          <p className="mt-1 text-xs text-slate-400">Leave blank if you already have an account.</p>
        </div>
        {error && <p className="text-sm text-red-600">{error}</p>}
        <button className="btn-primary w-full" disabled={busy}>
          {busy ? "Joining…" : "Join organization"}
        </button>
      </form>
    </AuthShell>
  );
}

export default function AcceptInvitePage() {
  return (
    <Suspense fallback={<div className="p-8 text-slate-400">Loading…</div>}>
      <AcceptInviteInner />
    </Suspense>
  );
}
