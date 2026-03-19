"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { IconAlertTriangle, IconLoader2, IconX } from "@tabler/icons-react";
import { Button } from "@/components/ui/button";
import { useCancelDeletion } from "@/features/dashboard/hooks/use-cancel-deletion";
import { formatDistanceToNow } from "date-fns";

interface Props {
  scheduledAt: string; // ISO timestamp from profile.scheduled_deletion_at
}

export function PendingDeletionBanner({ scheduledAt }: Props) {
  const router = useRouter();
  const [dismissed, setDismissed] = useState(false);

  const { cancelDeletion, isPending } = useCancelDeletion({
    onSuccess: () => router.refresh(),
  });

  if (dismissed) return null;

  const deletionDate = new Date(scheduledAt);
  const timeLeft = formatDistanceToNow(deletionDate, { addSuffix: true });

  return (
    <div className="flex items-center gap-3 border-b border-destructive/20 bg-destructive/[0.06] px-4 py-2.5 dark:bg-destructive/10">
      <IconAlertTriangle
        size={15}
        stroke={2}
        className="shrink-0 text-destructive"
      />
      <p className="flex-1 text-xs text-destructive">
        <span className="font-semibold">Account scheduled for deletion</span>
        {" — "}
        your account will be permanently deleted{" "}
        <span className="font-medium">{timeLeft}</span>. Cancel to keep your account.
      </p>
      <div className="flex shrink-0 items-center gap-2">
        <Button
          size="sm"
          variant="destructive"
          className="h-7 px-3 text-xs"
          onClick={cancelDeletion}
          disabled={isPending}
        >
          {isPending ? (
            <IconLoader2 size={13} stroke={2} className="animate-spin" data-icon="inline-start" />
          ) : null}
          Cancel deletion
        </Button>
        <button
          onClick={() => setDismissed(true)}
          className="text-destructive/60 transition-colors hover:text-destructive"
          aria-label="Dismiss"
        >
          <IconX size={14} stroke={2} />
        </button>
      </div>
    </div>
  );
}
