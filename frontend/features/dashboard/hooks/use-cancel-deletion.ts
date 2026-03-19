"use client";

import { useCallback, useTransition } from "react";
import { toast } from "@/components/ui/sonner";
import * as profileApi from "@/lib/api/profile";
import { parseApiError, errorMessage } from "@/lib/auth/errors";

interface Options {
  onSuccess?: () => void;
}

export function useCancelDeletion({ onSuccess }: Options = {}) {
  const [isPending, startTransition] = useTransition();

  const cancelDeletion = useCallback(() => {
    startTransition(async () => {
      try {
        await profileApi.cancelDeletion();
        toast.success("Account deletion cancelled. Your account is fully active again.");
        onSuccess?.();
      } catch (e) {
        const err = parseApiError(e);
        toast.error(errorMessage(err, "cancel_deletion"));
      }
    });
  }, [onSuccess]);

  return { cancelDeletion, isPending };
}
