"use client";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  IconDeviceDesktop, IconDeviceMobile, IconDeviceTablet,
  IconLoader2, IconLogout, IconMapPin, IconClock,
} from "@tabler/icons-react";
import { useSessions } from "@/features/settings/hooks/use-sessions";
import type { Session } from "@/lib/api/types";
import { formatDistanceToNow } from "date-fns";

function parseDevice(ua: string) {
  const u = ua.toLowerCase();
  if (u.includes("mobile") || u.includes("android") || u.includes("iphone"))
    return { icon: <IconDeviceMobile size={15} stroke={2} />, label: "Mobile" };
  if (u.includes("tablet") || u.includes("ipad"))
    return { icon: <IconDeviceTablet size={15} stroke={2} />, label: "Tablet" };
  return { icon: <IconDeviceDesktop size={15} stroke={2} />, label: "Desktop" };
}

function parseBrowser(ua: string) {
  if (/edg\//i.test(ua))       return "Edge";
  if (/opr\/|opera/i.test(ua)) return "Opera";
  if (/chrome/i.test(ua))      return "Chrome";
  if (/firefox/i.test(ua))     return "Firefox";
  if (/safari/i.test(ua))      return "Safari";
  if (/go-http/i.test(ua))     return "API client";
  return "Browser";
}

export function SessionsList({ sessions: initial }: { sessions: Session[] }) {
  const { sessions, revoke, revokingId } = useSessions(initial);

  if (sessions.length === 0) {
    return (
      <div className="flex items-center gap-3 py-4 text-sm text-muted-foreground">
        <IconDeviceDesktop size={20} stroke={1.5} className="shrink-0 opacity-40" />
        No active sessions found.
      </div>
    );
  }

  return (
    <div className="flex flex-col divide-y divide-border rounded-xl border border-border overflow-hidden">
      {sessions.map((session) => {
        const { icon, label } = parseDevice(session.user_agent);
        const browser = parseBrowser(session.user_agent);
        const isRevoking = revokingId === session.id;

        return (
          <div key={session.id} className="group/session flex items-center gap-3 bg-card px-4 py-3">
            <div className="flex size-9 shrink-0 items-center justify-center rounded-lg border border-border bg-muted/40 text-muted-foreground">
              {icon}
            </div>

            <div className="flex min-w-0 flex-1 flex-col gap-0.5">
              <div className="flex items-center gap-2">
                <span className="text-sm font-medium text-foreground">{browser} · {label}</span>
                {session.is_current && (
                  <Badge variant="secondary" className="text-[10px]">This device</Badge>
                )}
              </div>
              <div className="flex flex-wrap items-center gap-x-3 text-xs text-muted-foreground">
                {session.ip_address && (
                  <span className="flex items-center gap-1">
                    <IconMapPin size={10} stroke={2} />{session.ip_address}
                  </span>
                )}
                <span className="flex items-center gap-1">
                  <IconClock size={10} stroke={2} />
                  {formatDistanceToNow(new Date(session.last_active_at), { addSuffix: true })}
                </span>
              </div>
            </div>

            <Button
              variant="ghost" size="sm"
              disabled={isRevoking}
              onClick={() => revoke(session.id)}
              className="shrink-0 gap-1.5 text-xs text-muted-foreground opacity-0 transition-opacity group-hover/session:opacity-100 hover:bg-destructive/10 hover:text-destructive"
            >
              {isRevoking
                ? <IconLoader2 size={13} stroke={2} className="animate-spin" />
                : <IconLogout size={13} stroke={2} />}
              {session.is_current ? "Sign out" : "Revoke"}
            </Button>
          </div>
        );
      })}
    </div>
  );
}
