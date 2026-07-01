"use client";

import { useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { api, ApiError } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { AuthShell } from "@/components/AuthShell";

export default function SignupPage() {
  const { apply } = useAuth();
  const router = useRouter();
  const [form, setForm] = useState({ full_name: "", organization: "", email: "", password: "" });
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  function set(k: string, v: string) {
    setForm((f) => ({ ...f, [k]: v }));
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError("");
    setBusy(true);
    try {
      const p = await api.signup(form);
      apply(p);
      router.replace("/dashboard");
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Sign up failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <AuthShell title="Create your organization">
      <form onSubmit={submit} className="space-y-4">
        <div>
          <label className="label">Your name</label>
          <input className="input" value={form.full_name} onChange={(e) => set("full_name", e.target.value)} required />
        </div>
        <div>
          <label className="label">Organization name</label>
          <input className="input" value={form.organization} onChange={(e) => set("organization", e.target.value)} required />
        </div>
        <div>
          <label className="label">Work email</label>
          <input className="input" type="email" value={form.email} onChange={(e) => set("email", e.target.value)} required />
        </div>
        <div>
          <label className="label">Password</label>
          <input className="input" type="password" value={form.password} onChange={(e) => set("password", e.target.value)} required minLength={8} />
          <p className="mt-1 text-xs text-slate-400">At least 8 characters.</p>
        </div>
        {error && <p className="text-sm text-red-600">{error}</p>}
        <button className="btn-primary w-full" disabled={busy}>
          {busy ? "Creating…" : "Create account"}
        </button>
      </form>
      <p className="mt-4 text-center text-sm text-slate-500">
        Already have an account?{" "}
        <Link href="/login" className="text-brand-600 hover:underline">
          Sign in
        </Link>
      </p>
    </AuthShell>
  );
}
