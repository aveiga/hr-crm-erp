"use client";

import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { api, ApiError, userAdmin, type MemberPerm } from "@/lib/api";
import type { TeamInvite, TeamUser } from "@/lib/types";
import { useAuth, isAdmin } from "@/lib/auth";
import { formatDate } from "@/lib/format";
import { Icon } from "@/components/Icon";

// User manager (admin/owner only): members, invites and section access.
// Every guard is enforced server-side; the UI only hides what would be refused.

const ROLES = ["owner", "admin", "member", "viewer"] as const;

function roleOptions(actorRole: string, current?: string) {
  // Only owners may grant owner; keep the current value selectable for display.
  return ROLES.filter((r) => r !== "owner" || actorRole === "owner" || current === "owner");
}

function fullAccessRole(role: string) {
  return role === "owner" || role === "admin";
}

function errMsg(e: unknown, fallback: string) {
  return e instanceof ApiError ? e.message : fallback;
}

function accessLabel(role: string, groups: string[] | null) {
  if (fullAccessRole(role) || groups === null) return "Full access";
  if (groups.length === 0) return "No sections";
  return groups.join(", ");
}

function link(path: string) {
  return `${window.location.origin}${path}`;
}

export default function UsersPage() {
  const { role, user } = useAuth();
  const admin = isAdmin(role);
  const [groups, setGroups] = useState<string[]>([]);
  const [users, setUsers] = useState<TeamUser[]>([]);
  const [invites, setInvites] = useState<TeamInvite[]>([]);
  const [error, setError] = useState("");
  const [shared, setShared] = useState<{ label: string; url: string } | null>(null);
  const [editingUser, setEditingUser] = useState<TeamUser | null>(null);
  const [editingInvite, setEditingInvite] = useState<TeamInvite | null>(null);
  const [version, setVersion] = useState(0); // bumps to refresh the matrix

  function load() {
    setVersion((v) => v + 1);
    userAdmin
      .users()
      .then((r) => {
        setGroups(r.groups || []);
        setUsers(r.data || []);
      })
      .catch((e) => setError(errMsg(e, "Could not load members")));
    userAdmin
      .invites()
      .then((r) => setInvites(r.data || []))
      .catch(() => {});
  }

  useEffect(() => {
    if (admin) load();
  }, [admin]);

  if (!admin) {
    return (
      <div className="max-w-3xl">
        <h1 className="text-2xl font-bold">Users</h1>
        <p className="mt-2 text-sm text-slate-500">Only owners and admins can manage users.</p>
      </div>
    );
  }

  async function run(fn: () => Promise<unknown>, fallback: string) {
    setError("");
    try {
      await fn();
      load();
    } catch (e) {
      setError(errMsg(e, fallback));
    }
  }

  function resetPassword(u: TeamUser) {
    if (!confirm(`Create a one-time password reset link for ${u.email}? It expires in 24 hours.`)) return;
    run(async () => {
      const r = await userAdmin.passwordReset(u.user_id);
      setShared({ label: `Password reset link for ${u.email} (valid 24h, single use)`, url: link(r.path) });
    }, "Could not create reset link");
  }

  function removeUser(u: TeamUser) {
    if (!confirm(`Remove ${u.full_name || u.email} from this organization?`)) return;
    run(() => userAdmin.removeUser(u.user_id), "Could not remove member");
  }

  function resend(i: TeamInvite) {
    run(async () => {
      const r = await userAdmin.resendInvite(i.id);
      setShared({ label: `New invite link for ${i.email}`, url: link(`/accept-invite?token=${r.token}`) });
    }, "Could not resend invite");
  }

  function revoke(i: TeamInvite) {
    if (!confirm(`Revoke the invite for ${i.email}? The link stops working.`)) return;
    run(() => userAdmin.revokeInvite(i.id), "Could not revoke invite");
  }

  const ownerOnly = (r: string) => r === "owner" && role !== "owner";

  return (
    <div className="max-w-5xl space-y-8">
      <div>
        <h1 className="text-2xl font-bold">Users</h1>
        <p className="text-sm text-slate-500">
          Manage who can access this organization, their role and which sections they see.
        </p>
      </div>

      {error && <p className="text-sm text-red-600">{error}</p>}
      {shared && <SharedLink label={shared.label} url={shared.url} onDismiss={() => setShared(null)} />}

      <section className="space-y-3">
        <h2 className="text-lg font-semibold">Members</h2>
        <div className="card overflow-x-auto">
          <table className="min-w-full text-sm">
            <thead>
              <tr className="bg-slate-50 text-left text-slate-500">
                <th className="px-4 py-2 font-medium">Name</th>
                <th className="px-4 py-2 font-medium">Email</th>
                <th className="px-4 py-2 font-medium">Role</th>
                <th className="px-4 py-2 font-medium">Section access</th>
                <th className="px-4 py-2 font-medium">Joined</th>
                <th className="px-4 py-2 font-medium">Last login</th>
                <th className="px-4 py-2 font-medium" />
              </tr>
            </thead>
            <tbody>
              {users.map((u) => {
                const self = u.user_id === user?.id;
                const locked = ownerOnly(u.role);
                return (
                  <tr key={u.user_id} className="border-t border-slate-100">
                    <td className="px-4 py-2">
                      {u.full_name || "—"}
                      {self && <span className="ml-1 text-xs text-slate-400">(you)</span>}
                    </td>
                    <td className="px-4 py-2">{u.email}</td>
                    <td className="px-4 py-2 capitalize">{u.role}</td>
                    <td className="px-4 py-2 text-slate-600">{accessLabel(u.role, u.allowed_groups)}</td>
                    <td className="px-4 py-2 text-slate-500">{formatDate(u.joined_at)}</td>
                    <td className="px-4 py-2 text-slate-500">
                      {u.last_login_at ? formatDate(u.last_login_at) : "Never"}
                    </td>
                    <td className="whitespace-nowrap px-4 py-2 text-right">
                      {locked ? (
                        <span className="text-xs text-slate-400">Owner-managed</span>
                      ) : (
                        <div className="flex justify-end gap-3">
                          <button className="text-brand-600 hover:underline" onClick={() => setEditingUser(u)}>
                            Edit
                          </button>
                          {u.identity_editable && (
                            <button className="text-brand-600 hover:underline" onClick={() => resetPassword(u)}>
                              Reset password
                            </button>
                          )}
                          {!self && (
                            <button className="text-red-600 hover:underline" onClick={() => removeUser(u)}>
                              Remove
                            </button>
                          )}
                        </div>
                      )}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      </section>

      <InviteForm
        groups={groups}
        actorRole={role}
        onInvited={(inv) => {
          setShared({ label: `Invite link for ${inv.email}`, url: link(`/accept-invite?token=${inv.token}`) });
          load();
        }}
      />

      <section className="space-y-3">
        <h2 className="text-lg font-semibold">Invites</h2>
        <p className="text-sm text-slate-500">
          Every invitation and its status. Failed attempts show problems people hit while creating their
          account (expired or revoked links, missing or weak passwords).
        </p>
        <div className="card overflow-x-auto">
          <table className="min-w-full text-sm">
            <thead>
              <tr className="bg-slate-50 text-left text-slate-500">
                <th className="px-4 py-2 font-medium">Email</th>
                <th className="px-4 py-2 font-medium">Role</th>
                <th className="px-4 py-2 font-medium">Section access</th>
                <th className="px-4 py-2 font-medium">Status</th>
                <th className="px-4 py-2 font-medium">Expires</th>
                <th className="px-4 py-2 font-medium">Failed attempts</th>
                <th className="px-4 py-2 font-medium" />
              </tr>
            </thead>
            <tbody>
              {invites.length === 0 && (
                <tr>
                  <td colSpan={7} className="px-4 py-6 text-center text-slate-400">
                    No invites.
                  </td>
                </tr>
              )}
              {invites.map((i) => {
                const locked = ownerOnly(i.role);
                return (
                  <tr key={i.id} className="border-t border-slate-100 align-top">
                    <td className="px-4 py-2">{i.email}</td>
                    <td className="px-4 py-2 capitalize">{i.role}</td>
                    <td className="px-4 py-2 text-slate-600">{accessLabel(i.role, i.allowed_groups)}</td>
                    <td className="px-4 py-2">
                      <StatusBadge status={i.status} />
                    </td>
                    <td className="px-4 py-2 text-slate-500">
                      {i.status === "accepted" ? "—" : i.expires_at ? formatDate(i.expires_at) : "Never"}
                    </td>
                    <td className="px-4 py-2">
                      {i.failed_attempts > 0 ? (
                        <div>
                          <span className="font-medium text-red-600">{i.failed_attempts}</span>
                          {i.last_error && <div className="text-xs text-slate-500">{i.last_error}</div>}
                          {i.last_attempt_at && (
                            <div className="text-xs text-slate-400">Last: {formatDate(i.last_attempt_at)}</div>
                          )}
                        </div>
                      ) : (
                        <span className="text-slate-400">0</span>
                      )}
                    </td>
                    <td className="whitespace-nowrap px-4 py-2 text-right">
                      {i.status !== "accepted" && !locked && (
                        <div className="flex justify-end gap-3">
                          {i.status === "pending" && (
                            <>
                              <button
                                className="text-brand-600 hover:underline"
                                onClick={() =>
                                  navigator.clipboard?.writeText(link(`/accept-invite?token=${i.token}`))
                                }
                              >
                                Copy link
                              </button>
                              <button className="text-brand-600 hover:underline" onClick={() => setEditingInvite(i)}>
                                Edit
                              </button>
                            </>
                          )}
                          <button className="text-brand-600 hover:underline" onClick={() => resend(i)}>
                            Resend
                          </button>
                          {i.status !== "revoked" && (
                            <button className="text-red-600 hover:underline" onClick={() => revoke(i)}>
                              Revoke
                            </button>
                          )}
                        </div>
                      )}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      </section>

      <PermissionsMatrix version={version} onChange={load} />

      {editingUser && (
        <EditUserModal
          target={editingUser}
          groups={groups}
          actorRole={role}
          onClose={() => setEditingUser(null)}
          onSaved={() => {
            setEditingUser(null);
            load();
          }}
        />
      )}
      {editingInvite && (
        <EditInviteModal
          invite={editingInvite}
          groups={groups}
          actorRole={role}
          onClose={() => setEditingInvite(null)}
          onSaved={() => {
            setEditingInvite(null);
            load();
          }}
        />
      )}
    </div>
  );
}

function StatusBadge({ status }: { status: TeamInvite["status"] }) {
  const cls: Record<TeamInvite["status"], string> = {
    pending: "bg-amber-50 text-amber-700",
    accepted: "bg-green-50 text-green-700",
    expired: "bg-slate-100 text-slate-500",
    revoked: "bg-red-50 text-red-700",
  };
  return <span className={`rounded px-2 py-0.5 text-xs font-medium capitalize ${cls[status]}`}>{status}</span>;
}

function SharedLink({ label, url, onDismiss }: { label: string; url: string; onDismiss: () => void }) {
  const [copied, setCopied] = useState(false);
  return (
    <div className="rounded-md border border-amber-300 bg-amber-50 p-4 text-sm">
      <div className="mb-2 font-medium text-amber-900">{label}. Share it with the person directly.</div>
      <div className="flex items-center gap-2">
        <code className="flex-1 overflow-x-auto rounded bg-white px-2 py-1 text-xs">{url}</code>
        <button
          type="button"
          className="btn-secondary"
          onClick={async () => {
            await navigator.clipboard?.writeText(url).catch(() => {});
            setCopied(true);
          }}
        >
          <Icon name={copied ? "check" : "copy"} className="h-4 w-4" /> {copied ? "Copied" : "Copy"}
        </button>
        <button type="button" className="text-slate-500 hover:underline" onClick={onDismiss}>
          Dismiss
        </button>
      </div>
    </div>
  );
}

// SectionPicker edits a section restriction. null means full access (every
// section, including ones added later); owners/admins always have full access.
function SectionPicker({
  groups,
  value,
  onChange,
  role,
}: {
  groups: string[];
  value: string[] | null;
  onChange: (v: string[] | null) => void;
  role: string;
}) {
  if (fullAccessRole(role)) {
    return <p className="text-sm text-slate-500">Owners and admins always have access to every section.</p>;
  }
  const checked = (g: string) => value === null || value.includes(g);
  function toggle(g: string) {
    const cur = value === null ? [...groups] : [...value];
    const next = cur.includes(g) ? cur.filter((x) => x !== g) : [...cur, g];
    onChange(next.length === groups.length ? null : groups.filter((x) => next.includes(x)));
  }
  return (
    <div className="space-y-2">
      <div className="flex flex-wrap gap-x-4 gap-y-2">
        {groups.map((g) => (
          <label key={g} className="flex items-center gap-2 text-sm">
            <input type="checkbox" className="checkbox" checked={checked(g)} onChange={() => toggle(g)} />
            {g}
          </label>
        ))}
      </div>
      <div className="flex gap-3 text-xs">
        <button type="button" className="text-brand-600 hover:underline" onClick={() => onChange(null)}>
          Full access
        </button>
        <button type="button" className="text-brand-600 hover:underline" onClick={() => onChange([])}>
          Clear
        </button>
        <span className="text-slate-400">
          {value === null ? "Full access, including sections added later." : `${value.length} of ${groups.length} sections`}
        </span>
      </div>
    </div>
  );
}

function InviteForm({
  groups,
  actorRole,
  onInvited,
}: {
  groups: string[];
  actorRole: string;
  onInvited: (inv: TeamInvite) => void;
}) {
  const [email, setEmail] = useState("");
  const [role, setRole] = useState("member");
  // Restricted by default: the admin opts sections in rather than out.
  const [sections, setSections] = useState<string[] | null>([]);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError("");
    setBusy(true);
    try {
      const inv = await userAdmin.invite({
        email,
        role,
        allowed_groups: fullAccessRole(role) ? null : sections,
      });
      setEmail("");
      setSections([]);
      onInvited(inv);
    } catch (err) {
      setError(errMsg(err, "Invite failed"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="space-y-3">
      <h2 className="text-lg font-semibold">Invite a teammate</h2>
      <form onSubmit={submit} className="card space-y-4 p-4">
        <div className="flex flex-wrap items-end gap-3">
          <div className="min-w-[220px] flex-1">
            <label className="label">Email</label>
            <input className="input" type="email" value={email} onChange={(e) => setEmail(e.target.value)} required />
          </div>
          <div>
            <label className="label">Role</label>
            <select className="select capitalize" value={role} onChange={(e) => setRole(e.target.value)}>
              {roleOptions(actorRole).map((r) => (
                <option key={r} value={r}>
                  {r}
                </option>
              ))}
            </select>
          </div>
        </div>
        <div>
          <label className="label">Section access</label>
          <SectionPicker groups={groups} value={sections} onChange={setSections} role={role} />
        </div>
        {error && <p className="text-sm text-red-600">{error}</p>}
        <button className="btn-primary" disabled={busy}>
          <Icon name="user-plus" className="h-4 w-4" /> {busy ? "Inviting…" : "Create invite"}
        </button>
      </form>
    </section>
  );
}

function Modal({ title, onClose, children }: { title: string; onClose: () => void; children: React.ReactNode }) {
  const [mounted, setMounted] = useState(false);
  useEffect(() => setMounted(true), []);
  if (!mounted) return null;
  return createPortal(
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4" onClick={onClose}>
      <div
        className="max-h-[85vh] w-full max-w-lg overflow-y-auto rounded-lg bg-white p-6 shadow-xl"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="mb-4 flex items-center justify-between">
          <h3 className="text-lg font-semibold">{title}</h3>
          <button type="button" className="text-slate-500 hover:text-slate-800" onClick={onClose}>
            <Icon name="x" className="h-5 w-5" />
          </button>
        </div>
        {children}
      </div>
    </div>,
    document.body
  );
}

function EditUserModal({
  target,
  groups,
  actorRole,
  onClose,
  onSaved,
}: {
  target: TeamUser;
  groups: string[];
  actorRole: string;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [fullName, setFullName] = useState(target.full_name);
  const [email, setEmail] = useState(target.email);
  const [role, setRole] = useState<string>(target.role);
  const [sections, setSections] = useState<string[] | null>(target.allowed_groups);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function save(e: React.FormEvent) {
    e.preventDefault();
    setError("");
    setBusy(true);
    try {
      const body: Parameters<typeof userAdmin.updateUser>[1] = { role };
      // Only send identity fields that changed (the server rejects identity
      // changes for users who belong to other organizations).
      if (fullName !== target.full_name) body.full_name = fullName;
      if (email !== target.email) body.email = email;
      if (!fullAccessRole(role)) body.allowed_groups = sections;
      await userAdmin.updateUser(target.user_id, body);
      onSaved();
    } catch (err) {
      setError(errMsg(err, "Update failed"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal title={`Edit ${target.full_name || target.email}`} onClose={onClose}>
      <form onSubmit={save} className="space-y-4">
        {!target.identity_editable && (
          <p className="rounded bg-slate-50 p-2 text-xs text-slate-500">
            This user also belongs to other organizations, so only they can change their name, email or
            password. You can still change their role and section access here.
          </p>
        )}
        <div>
          <label className="label">Full name</label>
          <input
            className="input"
            value={fullName}
            disabled={!target.identity_editable}
            onChange={(e) => setFullName(e.target.value)}
          />
        </div>
        <div>
          <label className="label">Email</label>
          <input
            className="input"
            type="email"
            value={email}
            disabled={!target.identity_editable}
            onChange={(e) => setEmail(e.target.value)}
            required
          />
        </div>
        <div>
          <label className="label">Role</label>
          <select className="select capitalize" value={role} onChange={(e) => setRole(e.target.value)}>
            {roleOptions(actorRole, target.role).map((r) => (
              <option key={r} value={r}>
                {r}
              </option>
            ))}
          </select>
        </div>
        <div>
          <label className="label">Section access</label>
          <SectionPicker groups={groups} value={sections} onChange={setSections} role={role} />
        </div>
        {error && <p className="text-sm text-red-600">{error}</p>}
        <div className="flex justify-end gap-2">
          <button type="button" className="btn-secondary" onClick={onClose}>
            Cancel
          </button>
          <button className="btn-primary" disabled={busy}>
            {busy ? "Saving…" : "Save"}
          </button>
        </div>
      </form>
    </Modal>
  );
}

function EditInviteModal({
  invite,
  groups,
  actorRole,
  onClose,
  onSaved,
}: {
  invite: TeamInvite;
  groups: string[];
  actorRole: string;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [role, setRole] = useState<string>(invite.role);
  const [sections, setSections] = useState<string[] | null>(invite.allowed_groups);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function save(e: React.FormEvent) {
    e.preventDefault();
    setError("");
    setBusy(true);
    try {
      await userAdmin.updateInvite(invite.id, {
        role,
        allowed_groups: fullAccessRole(role) ? null : sections,
      });
      onSaved();
    } catch (err) {
      setError(errMsg(err, "Update failed"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal title={`Edit invite for ${invite.email}`} onClose={onClose}>
      <form onSubmit={save} className="space-y-4">
        <div>
          <label className="label">Role</label>
          <select className="select capitalize" value={role} onChange={(e) => setRole(e.target.value)}>
            {roleOptions(actorRole, invite.role).map((r) => (
              <option key={r} value={r}>
                {r}
              </option>
            ))}
          </select>
        </div>
        <div>
          <label className="label">Section access</label>
          <SectionPicker groups={groups} value={sections} onChange={setSections} role={role} />
        </div>
        {error && <p className="text-sm text-red-600">{error}</p>}
        <div className="flex justify-end gap-2">
          <button type="button" className="btn-secondary" onClick={onClose}>
            Cancel
          </button>
          <button className="btn-primary" disabled={busy}>
            {busy ? "Saving…" : "Save"}
          </button>
        </div>
      </form>
    </Modal>
  );
}

// PermissionsMatrix is the per-member section-access grid (moved from /team).
// Owners/admins always have full access.
function PermissionsMatrix({ version, onChange }: { version: number; onChange: () => void }) {
  const [groups, setGroups] = useState<string[]>([]);
  const [members, setMembers] = useState<MemberPerm[]>([]);
  const [error, setError] = useState("");

  function load() {
    api
      .permissions()
      .then((r) => {
        setGroups(r.groups);
        setMembers(r.members);
      })
      .catch(() => {});
  }
  useEffect(load, [version]);

  function isChecked(m: MemberPerm, g: string) {
    if (fullAccessRole(m.role) || m.allowed_groups === null) return true;
    return m.allowed_groups.includes(g);
  }

  async function set(m: MemberPerm, payload: string[] | null) {
    setError("");
    setMembers((ms) => ms.map((x) => (x.user_id === m.user_id ? { ...x, allowed_groups: payload } : x)));
    try {
      await api.setPermissions(m.user_id, payload);
      onChange();
    } catch (e) {
      setError(errMsg(e, "Update failed"));
      load();
    }
  }

  function toggle(m: MemberPerm, g: string) {
    if (fullAccessRole(m.role)) return;
    const current = m.allowed_groups === null ? [...groups] : [...m.allowed_groups];
    const next = current.includes(g) ? current.filter((x) => x !== g) : [...current, g];
    // All sections checked == full (unrestricted) access.
    set(m, next.length === groups.length ? null : next);
  }

  return (
    <section className="space-y-3">
      <h2 className="text-lg font-semibold">Section access</h2>
      <p className="text-sm text-slate-500">
        Quick view of which sections each member can see. Owners and admins always have full access. A
        member with every box checked has full access (including sections added later).
      </p>
      {error && <p className="text-sm text-red-600">{error}</p>}
      <div className="card overflow-x-auto">
        <table className="min-w-full text-sm">
          <thead>
            <tr className="bg-slate-50 text-left text-slate-500">
              <th className="px-4 py-2 font-medium">Member</th>
              {groups.map((g) => (
                <th key={g} className="px-3 py-2 text-center font-medium">
                  {g}
                </th>
              ))}
              <th className="px-3 py-2" />
            </tr>
          </thead>
          <tbody>
            {members.map((m) => {
              const full = fullAccessRole(m.role);
              return (
                <tr key={m.user_id} className="border-t border-slate-100">
                  <td className="px-4 py-2">
                    <div className="font-medium">{m.full_name || m.email}</div>
                    <div className="text-xs capitalize text-slate-400">{m.role}</div>
                  </td>
                  {groups.map((g) => (
                    <td key={g} className="px-3 py-2 text-center">
                      <input
                        type="checkbox"
                        className="checkbox"
                        checked={isChecked(m, g)}
                        disabled={full}
                        onChange={() => toggle(m, g)}
                      />
                    </td>
                  ))}
                  <td className="px-3 py-2 text-right">
                    {!full && m.allowed_groups !== null && (
                      <button className="text-brand-600 hover:underline" onClick={() => set(m, null)}>
                        Full access
                      </button>
                    )}
                    {full && <span className="text-xs text-slate-400">Full access</span>}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </section>
  );
}
