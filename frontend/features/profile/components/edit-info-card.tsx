"use client";

import { useState, useEffect } from "react";
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Separator } from "@/components/ui/separator";
import { Badge } from "@/components/ui/badge";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from "@/components/ui/dialog";
import {
  IconCamera,
  IconPencil,
  IconLoader2,
  IconAt,
  IconUser,
  IconMail,
  IconCircleCheck,
  IconCircleX,
  IconCheck,
} from "@tabler/icons-react";
import { cn, getInitials } from "@/lib/utils";
import { useUpdateProfile } from "@/features/profile/hooks/use-update-profile";
import { useUpdateUsername } from "@/features/profile/hooks/use-update-username";
import { EmailChangeDialog } from "@/features/settings/components/email-change-dialog";
import { FieldRow } from "@/features/shared/components/form-components";
import { useDebounce } from "@/hooks/shared/use-debounce";
import type { UserProfile } from "@/lib/api/types";

function AvatarBanner({ profile }: { profile: UserProfile | null }) {
  const initials = getInitials(profile?.display_name ?? "");

  return (
    <div className="flex flex-col items-center gap-3 py-6">
      <div className="group/avatar relative">
        <Avatar className="size-20 ring-4 ring-background shadow-md">
          {profile?.avatar_url && (
            <AvatarImage src={profile.avatar_url} alt={profile.display_name} />
          )}
          <AvatarFallback className="bg-primary text-primary-foreground text-2xl font-bold">
            {initials}
          </AvatarFallback>
        </Avatar>
        <button
          disabled
          title="Avatar upload coming soon"
          className="absolute inset-0 flex flex-col items-center justify-center rounded-full bg-black/0 transition-all duration-200 group-hover/avatar:bg-black/50 cursor-not-allowed"
        >
          <IconCamera
            size={20}
            stroke={1.75}
            className="text-white opacity-0 transition-opacity duration-200 group-hover/avatar:opacity-100"
          />
        </button>
      </div>
      <div className="flex flex-col items-center gap-0.5 text-center">
        <p className="text-sm font-medium text-foreground">
          {profile?.display_name ?? "—"}
        </p>
        <p className="text-xs text-muted-foreground">{profile?.email ?? "—"}</p>
        <button
          disabled
          className="mt-1.5 text-xs font-medium text-primary/50 cursor-not-allowed"
        >
          Change photo{" "}
          <span className="font-normal text-muted-foreground">
            (coming soon)
          </span>
        </button>
      </div>
    </div>
  );
}

function EditDisplayNameDialog({
  open,
  onOpenChange,
  current,
  onSave,
  isPending,
}: {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  current: string;
  onSave: (v: string) => void;
  isPending: boolean;
}) {
  const [value, setValue] = useState(current);
  const [error, setError] = useState<string | null>(null);

  function handleOpenChange(v: boolean) {
    if (v) {
      setValue(current);
      setError(null);
    }
    onOpenChange(v);
  }

  function handleSave() {
    const t = value.trim();
    if (!t) {
      setError("Display name cannot be empty.");
      return;
    }
    if (t.length > 60) {
      setError("Display name is too long.");
      return;
    }
    if (t === current) {
      onOpenChange(false);
      return;
    }
    onSave(t);
  }

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Edit display name</DialogTitle>
          <DialogDescription>
            This is the name shown across the dashboard.
          </DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-1.5">
          <label className="text-xs font-medium text-muted-foreground">
            Display name
          </label>
          <Input
            ref={(el) => {
              if (open) el?.focus();
            }}
            value={value}
            onChange={(e) => {
              setValue(e.target.value);
              setError(null);
            }}
            onKeyDown={(e) => e.key === "Enter" && handleSave()}
            placeholder="Your full name"
            aria-invalid={!!error}
          />
          {error && <p className="text-xs text-destructive">{error}</p>}
        </div>
        <DialogFooter>
          <Button
            variant="outline"
            size="sm"
            onClick={() => onOpenChange(false)}
            disabled={isPending}
          >
            Cancel
          </Button>
          <Button
            size="sm"
            onClick={handleSave}
            disabled={isPending || !value.trim()}
          >
            {isPending && (
              <IconLoader2
                size={14}
                stroke={2}
                className="animate-spin"
                data-icon="inline-start"
              />
            )}
            Save
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function EditUsernameDialog({
  open,
  onOpenChange,
  current,
  onSuccess,
}: {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  current: string;
  onSuccess: (u: string) => void;
}) {
  const [value, setValue] = useState(current);
  const debouncedValue = useDebounce(value, 400);

  const {
    updateUsername,
    isPending,
    checkAvailability,
    isAvailable,
    isChecking,
  } = useUpdateUsername({
    onSuccess: (u) => {
      onOpenChange(false);
      onSuccess(u);
    },
  });

  // Check availability when debounced value changes
  useEffect(() => {
    const trimmed = debouncedValue.trim();
    if (trimmed && trimmed !== current && trimmed.length >= 3) {
      checkAvailability(trimmed);
    }
  }, [debouncedValue, current, checkAvailability]);

  function handleOpenChange(v: boolean) {
    if (v) setValue(current);
    onOpenChange(v);
  }

  function handleSave() {
    const t = value.trim();
    if (t === current) {
      onOpenChange(false);
      return;
    }
    if (!t || t.length < 3 || isAvailable === false) return;
    updateUsername(t);
  }

  const isDirty = value.trim() !== current;
  const canSave = isDirty && value.trim().length >= 3 && isAvailable !== false;

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Edit username</DialogTitle>
          <DialogDescription>
            3–30 characters. Letters, numbers, and underscores only.
          </DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-1.5">
          <label className="text-xs font-medium text-muted-foreground">
            Username
          </label>
          <div className="relative">
            <Input
              ref={(el) => {
                if (open) el?.focus();
              }}
              value={value}
              onChange={(e) => setValue(e.target.value)}
              onKeyDown={(e) => e.key === "Enter" && handleSave()}
              placeholder="e.g. johndoe"
              className={cn(
                "pr-8",
                isAvailable === true && "border-primary/60",
                isAvailable === false && "border-destructive/50",
              )}
              aria-invalid={isAvailable === false}
            />
            {isDirty && (
              <span className="absolute right-2.5 top-1/2 -translate-y-1/2">
                {isChecking ? (
                  <IconLoader2
                    size={14}
                    stroke={2}
                    className="animate-spin text-muted-foreground"
                  />
                ) : isAvailable === true ? (
                  <IconCircleCheck
                    size={14}
                    stroke={2}
                    className="text-primary"
                  />
                ) : isAvailable === false ? (
                  <IconCircleX
                    size={14}
                    stroke={2}
                    className="text-destructive"
                  />
                ) : null}
              </span>
            )}
          </div>
          {isAvailable === false ? (
            <p className="text-xs text-destructive">
              Username is already taken.
            </p>
          ) : isAvailable === true ? (
            <p className="text-xs text-primary">Username is available.</p>
          ) : null}
        </div>
        <DialogFooter>
          <Button
            variant="outline"
            size="sm"
            onClick={() => onOpenChange(false)}
            disabled={isPending}
          >
            Cancel
          </Button>
          <Button
            size="sm"
            onClick={handleSave}
            disabled={isPending || !canSave}
          >
            {isPending && (
              <IconLoader2
                size={14}
                stroke={2}
                className="animate-spin"
                data-icon="inline-start"
              />
            )}
            Save
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export function EditInfoCard({ profile }: { profile: UserProfile | null }) {
  const [displayName, setDisplayName] = useState(profile?.display_name ?? "");
  const [username, setUsername] = useState(profile?.username ?? "");
  const [email, setEmail] = useState(profile?.email ?? "");
  const [nameDialogOpen, setNameDialogOpen] = useState(false);
  const [usernameDialogOpen, setUsernameDialogOpen] = useState(false);
  const [emailDialogOpen, setEmailDialogOpen] = useState(false);

  const { updateDisplayName, isPending: updatingName } = useUpdateProfile({
    onSuccess: () => setNameDialogOpen(false),
  });

  return (
    <>
      <AvatarBanner profile={profile} />
      <Separator />
      <div className="flex flex-col">
        <FieldRow
          icon={<IconUser size={15} stroke={2} />}
          label="Display name"
          value={displayName}
          onEditAction={() => setNameDialogOpen(true)}
        />
        <Separator />
        <FieldRow
          icon={<IconAt size={15} stroke={2} />}
          label="Username"
          value={username ? `@${username}` : ""}
          onEditAction={() => setUsernameDialogOpen(true)}
        />
        <Separator />
        <FieldRow
          icon={<IconMail size={15} stroke={2} />}
          label="Email address"
          value={email}
          onEditAction={() => setEmailDialogOpen(true)}
          suffix={
            profile?.email_verified ? (
              <Badge variant="secondary" className="gap-1 text-[10px]">
                <IconCircleCheck size={10} stroke={2.5} />
                Verified
              </Badge>
            ) : (
              <Badge variant="destructive" className="text-[10px]">
                Unverified
              </Badge>
            )
          }
        />
      </div>
      <EditDisplayNameDialog
        open={nameDialogOpen}
        onOpenChange={setNameDialogOpen}
        current={displayName}
        onSave={(v) => {
          setDisplayName(v);
          updateDisplayName(v);
        }}
        isPending={updatingName}
      />
      <EditUsernameDialog
        open={usernameDialogOpen}
        onOpenChange={setUsernameDialogOpen}
        current={username}
        onSuccess={setUsername}
      />
      <EmailChangeDialog
        open={emailDialogOpen}
        onOpenChange={setEmailDialogOpen}
        currentEmail={email}
        onSuccess={(newEmail) => {
          setEmail(newEmail);
          setEmailDialogOpen(false);
        }}
      />
    </>
  );
}
