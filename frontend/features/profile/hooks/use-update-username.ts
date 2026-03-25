"use client";

import { useCallback, useTransition, useState } from "react";
import { toast } from "@/components/ui/sonner";
import * as profileApi from "@/lib/api/profile";
import { parseApiError, errorMessage } from "@/lib/auth/errors";

interface Options {
  onSuccess?: (username: string) => void;
}

export function useUpdateUsername({ onSuccess }: Options = {}) {
  const [isPending, startTransition] = useTransition();
  const [isAvailable, setIsAvailable] = useState<boolean | null>(null);
  const [isChecking, setIsChecking] = useState(false);

  const checkAvailability = useCallback(async (username: string) => {
    if (!username || username.length < 3) {
      setIsAvailable(null);
      return;
    }
    setIsChecking(true);
    try {
      const res = await profileApi.checkUsernameAvailable(username);
      setIsAvailable(res.data.available);
    } catch {
      setIsAvailable(null);
    } finally {
      setIsChecking(false);
    }
  }, []);

  const updateUsername = useCallback(
    (username: string) => {
      startTransition(async () => {
        try {
          await profileApi.updateUsername({ username });
          toast.success("Username updated.");
          setIsAvailable(null);
          onSuccess?.(username);
        } catch (e) {
          const err = parseApiError(e);
          toast.error(errorMessage(err, "update_username"));
        }
      });
    },
    [onSuccess],
  );

  return { updateUsername, isPending, checkAvailability, isAvailable, isChecking };
}
