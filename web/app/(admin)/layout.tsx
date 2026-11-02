"use client";

import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { ReactNode, useEffect, useState } from "react";
import { DeliveryProvider, useDelivery } from "@/components/DeliveryContext";
import { Icon, IconName } from "@/components/Icon";
import { ToastProvider } from "@/components/Toast";
import { LiveDot } from "@/components/ui";
import { api, sessionToken } from "@/lib/api";

export default function AdminLayout({ children }: { children: ReactNode }) {
  const router = useRouter();
  const pathname = usePathname();
  const [session, setSession] = useState<"checking" | "ok">("checking");

  useEffect(() => {
    const toLogin = () => router.replace(`/login?next=${encodeURIComponent(window.location.pathname)}`);
    api("/api/v1/auth/session").then(() => setSession("ok")).catch(toLogin);
    window.addEventListener("session-expired", toLogin);
    return () => window.removeEventListener("session-expired", toLogin);
  }, [router]);

  if (session === "checking") {
    return <div className="boot"><span className="brand-mark is-pulsing">AI</span></div>;
  }
  return (
    <ToastProvider>
      <DeliveryProvider>
        <div className="shell">
          <Sidebar pathname={pathname} onLogout={async () => {
            await api("/api/v1/auth/logout", { method: "POST" }).catch(() => undefined);
            sessionToken.clear();
            router.replace("/login");
          }} />
          <main className="workspace" key={pathname}>{children}</main>
        </div>
      </DeliveryProvider>
    </ToastProvider>
  );
}

const nav: { href: string; label: string; icon: IconName; match: (path: string) => boolean }[] = [
  { href: "/", label: "Overview", icon: "overview", match: path => path === "/" },
  { href: "/campaigns", label: "Campaigns", icon: "campaigns", match: path => path.startsWith("/campaigns") && path !== "/campaigns/new" },
  { href: "/settings", label: "Settings", icon: "settings", match: path => path.startsWith("/settings") },
];

function Sidebar({ pathname, onLogout }: { pathname: string; onLogout: () => void }) {
  const { data: status, error } = useDelivery();
  const active = status ? status.queue.ready + status.queue.published + status.queue.sending : 0;
  return (
    <aside className="sidebar">
      <Link href="/" className="brand">
        <span className="brand-mark">AI</span>
        <span>AI4ALL<small>Certificate operations</small></span>
      </Link>
      <Link href="/campaigns/new" className={`new-campaign ${pathname === "/campaigns/new" ? "is-active" : ""}`}>
        <Icon name="plus" size={16} /> New campaign
      </Link>
      <nav>
        {nav.map(item => (
          <Link key={item.href} href={item.href} className={item.match(pathname) ? "is-active" : ""}>
            <Icon name={item.icon} size={17} />{item.label}
            {item.href === "/" && active > 0 && <span className="nav-count">{active}</span>}
          </Link>
        ))}
      </nav>
      <div className="system-card">
        <div className="system-row">
          <span>Delivery</span>
          {error ? <strong className="is-bad">Offline</strong>
            : <strong><LiveDot active={(status?.workers.online ?? 0) > 0} />{status ? `${status.workers.online} workers` : "…"}</strong>}
        </div>
        <div className="system-row">
          <span>SMTP</span>
          <strong className={status && !status.smtpConfigured ? "is-warn" : ""}>{status ? (status.smtpConfigured ? "Configured" : "Not set") : "…"}</strong>
        </div>
        {status && status.limits.dailyLimit > 0 && (
          <div className="system-quota">
            <div className="system-row"><span>Daily quota</span><strong>{status.limits.dailyUsed} / {status.limits.dailyLimit}</strong></div>
            <div className="quota-bar"><i style={{ width: `${Math.min(100, (status.limits.dailyUsed / status.limits.dailyLimit) * 100)}%` }} /></div>
          </div>
        )}
      </div>
      <button className="logout" onClick={onLogout}><Icon name="logout" size={16} /> Sign out</button>
    </aside>
  );
}
