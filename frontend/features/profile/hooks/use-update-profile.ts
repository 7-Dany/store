"use client";

import { useCallback, useTransition } from "react";
import { toast } from "@/components/ui/sonner";
import * as profileApi from "@/lib/api/profile";
import { parseApiError, errorMessage } from "@/lib/auth/errors";

interface Options {
  onSuccess?: () => void;
}

export function useUpdateProfile({ onSuccess }: Options = {}) {
  const [isPending, startTransition] = useTransition();

  const updateDisplayName = useCallback(
    (display_name: string) => {
      startTransition(async () => {
        try {
          await profileApi.updateProfile({ display_name });
          toast.success("Display name updated.");
          onSuccess?.();
        } catch (e) {
          const err = parseApiError(e);
          toast.error(errorMessage(err, "update_profile"));
        }
      });
    },
    [onSuccess],
  );

  return { updateDisplayName, isPending };
}
