"use client";

import { useCallback, useTransition } from "react";
import { toast } from "@/components/ui/sonner";
import * as authApi from "@/lib/api/auth";
import { parseApiError, errorMessage } from "@/lib/auth/errors";

interface Options {
  onSuccess?: () => void;
}

export function useChangePassword({ onSuccess }: Options = {}) {
  const [isPending, startTransition] = useTransition();

  const changePassword = useCallback(
    (old_password: string, new_password: string) => {
      startTransition(async () => {
        try {
          await authApi.changePassword({ old_password, new_password });
          toast.success("Password changed. You'll be signed out of all other devices.");
          onSuccess?.();
        } catch (e) {
          const err = parseApiError(e);
          toast.error(errorMessage(err, "change_password"));
        }
      });
    },
    [onSuccess],
  );

  return { changePassword, isPending };
}
