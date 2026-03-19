"use client";

import { useState } from "react";
import { buttonVariants } from "@/components/ui/button";
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from "@/components/ui/sheet";
import { IconUser, IconMenu2 } from "@tabler/icons-react";
import { cn } from "@/lib/utils";

const SECTIONS = [
  { id: "profile", label: "Profile", icon: IconUser },
] as const;

type SectionId = (typeof SECTIONS)[number]["id"];

function scrollTo(id: string, closeSheet: () => void) {
  document.getElementById(id)?.scrollIntoView({ behavior: "smooth", block: "start" });
  // Close sheet after a brief delay for smooth transition
  setTimeout(() => closeSheet(), 300);
}

export function SettingsMobileNav() {
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState<SectionId>("profile");

  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetTrigger 
        id="settings-mobile-nav-trigger"
        className={buttonVariants({ variant: "outline", size: "sm", className: "w-full justify-start gap-2" })}
      >
        <IconMenu2 size={16} stroke={2} />
        <span>Settings menu</span>
      </SheetTrigger>
      <SheetContent side="left" className="w-64">
        <SheetHeader>
          <SheetTitle>Settings</SheetTitle>
        </SheetHeader>
        <nav className="mt-6 flex flex-col gap-1" role="navigation" aria-label="Settings navigation">
          {SECTIONS.map(({ id, label, icon: Icon }) => (
            <button
              key={id}
              onClick={() => {
                setActive(id);
                scrollTo(id, () => setOpen(false));
              }}
              aria-current={active === id ? "page" : undefined}
              aria-label={`Navigate to ${label} section`}
              className={cn(
                "flex items-center gap-3 rounded-lg px-3 py-2 text-sm transition-colors",
                active === id
                  ? "bg-primary text-primary-foreground"
                  : "text-muted-foreground hover:bg-muted hover:text-foreground"
              )}
            >
              <Icon size={18} stroke={1.5} aria-hidden="true" />
              <span>{label}</span>
            </button>
          ))}
        </nav>
      </SheetContent>
    </Sheet>
  );
}
