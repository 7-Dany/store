"use client";

import { useCallback, useState, useTransition } from "react";
import { toast } from "@/components/ui/sonner";
import * as profileApi from "@/lib/api/profile";
import { parseApiError, errorMessage } from "@/lib/auth/errors";
import type { Session } from "@/lib/api/types";

export function useSessions(initialSessions: Session[]) {
  const [sessions, setSessions] = useState<Session[]>(initialSessions);
  const [isPending, startTransition] = useTransition();
  const [revokingId, setRevokingId] = useState<string | null>(null);

  const revoke = useCallback((sessionId: string) => {
    setRevokingId(sessionId);
    startTransition(async () => {
      try {
        await profileApi.revokeSession(sessionId);
        setSessions((prev) => prev.filter((s) => s.id !== sessionId));
        toast.success("Session revoked.");
      } catch (e) {
        const err = parseApiError(e);
        toast.error(errorMessage(err, "revoke_session"));
      } finally {
        setRevokingId(null);
      }
    });
  }, []);

  return { sessions, revoke, isPending, revokingId };
}
