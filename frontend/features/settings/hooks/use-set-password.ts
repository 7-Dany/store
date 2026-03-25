"use client";

import { useCallback, useTransition } from "react";
import { toast } from "@/components/ui/sonner";
import * as profileApi from "@/lib/api/profile";
import { parseApiError, errorMessage } from "@/lib/auth/errors";

interface Options {
  onSuccess?: () => void;
}

export function useSetPassword({ onSuccess }: Options = {}) {
  const [isPending, startTransition] = useTransition();

  const setPassword = useCallback(
    (new_password: string) => {
      startTransition(async () => {
        try {
          await profileApi.setPassword({ new_password });
          toast.success("Password set. You can now sign in with your email and password.");
          onSuccess?.();
        } catch (e) {
          const err = parseApiError(e);
          toast.error(errorMessage(err, "set_password"));
        }
      });
    },
    [onSuccess],
  );

  return { setPassword, isPending };
}
