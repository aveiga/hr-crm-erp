import "./globals.css";
import type { Metadata } from "next";
import { AuthProvider } from "@/lib/auth";

export const metadata: Metadata = {
  title: "HR · CRM · ERP",
  description: "Manage teams, leads, offers and service agreements.",
};

// The browser API base URL is resolved at runtime (not baked at build time) so
// the same image can be deployed anywhere by setting the API_URL env var on the
// container. Reading process.env below forces per-request rendering, so the
// injected value reflects the running environment rather than the build one.
export const dynamic = "force-dynamic";

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  const apiUrl = process.env.API_URL || "http://localhost:8080";
  return (
    <html lang="en">
      <body>
        {/* Runtime config for the client SPA; read in lib/api.ts. This inline
            script runs at HTML parse time, before any bundled JS. */}
        <script
          dangerouslySetInnerHTML={{
            __html: `window.__ENV__=${JSON.stringify({ apiUrl })}`,
          }}
        />
        <AuthProvider>{children}</AuthProvider>
      </body>
    </html>
  );
}
