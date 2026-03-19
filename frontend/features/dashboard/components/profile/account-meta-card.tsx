import {
  Card,
  CardHeader,
  CardTitle,
  CardDescription,
  CardContent,
} from "@/components/ui/card";
import { Separator } from "@/components/ui/separator";
import { Badge } from "@/components/ui/badge";
import {
  IconCalendar,
  IconLogin2,
  IconShieldCheck,
  IconCircleX,
} from "@tabler/icons-react";
import { formatDate } from "@/lib/utils";
import type { UserProfile } from "@/lib/api/types";

interface Props {
  profile: UserProfile | null;
}

function Row({
  icon,
  label,
  value,
  badge,
}: {
  icon: React.ReactNode;
  label: string;
  value?: string;
  badge?: React.ReactNode;
}) {
  return (
    <div className="flex items-center gap-3 py-2.5 text-sm">
      <div className="flex size-7 shrink-0 items-center justify-center rounded-md border border-border bg-muted/40 text-muted-foreground">
        {icon}
      </div>
      <span className="min-w-0 flex-1 text-xs text-muted-foreground">{label}</span>
      {badge ?? (
        <span className="text-xs font-medium text-foreground">{value ?? "—"}</span>
      )}
    </div>
  );
}

export function AccountMetaCard({ profile }: Props) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>Account details</CardTitle>
        <CardDescription>Read-only account metadata.</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col">
        <Row
          icon={<IconCalendar size={14} stroke={2} />}
          label="Joined"
          value={profile?.created_at
            ? formatDate(profile.created_at, { month: "short", day: "numeric", year: "numeric" })
            : "—"}
        />
        <Separator />
        <Row
          icon={<IconLogin2 size={14} stroke={2} />}
          label="Last login"
          value={profile?.last_login_at
            ? formatDate(profile.last_login_at, { month: "short", day: "numeric", year: "numeric" })
            : "—"}
        />
        <Separator />
        <Row
          icon={<IconShieldCheck size={14} stroke={2} />}
          label="Email verified"
          badge={
            profile?.email_verified ? (
              <Badge variant="secondary" className="gap-1 text-[10px]">
                <IconShieldCheck size={10} stroke={2.5} />
                Yes
              </Badge>
            ) : (
              <Badge variant="destructive" className="gap-1 text-[10px]">
                <IconCircleX size={10} stroke={2.5} />
                No
              </Badge>
            )
          }
        />
      </CardContent>
    </Card>
  );
}
