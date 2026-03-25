"use client";

import { useEffect, useRef, useState } from "react";
import {
  SidebarGroup,
  SidebarGroupLabel,
  SidebarMenu,
  SidebarMenuItem,
  SidebarMenuButton,
} from "@/components/ui/sidebar";
import { IconUser } from "@tabler/icons-react";

const SECTIONS = [
  { id: "profile", label: "Profile", icon: IconUser },
] as const;

type SectionId = (typeof SECTIONS)[number]["id"];

function scrollTo(id: string) {
  document.getElementById(id)?.scrollIntoView({ behavior: "smooth", block: "start" });
}

export function SettingsNav() {
  const [active, setActive] = useState<SectionId>("profile");
  const ioRef = useRef<IntersectionObserver | null>(null);

  useEffect(() => {
    const root = document.querySelector("[data-settings-scroll]") as HTMLElement | null;

    ioRef.current = new IntersectionObserver(
      (entries) => {
        const visible = entries
          .filter((e) => e.isIntersecting)
          .sort((a, b) => a.boundingClientRect.top - b.boundingClientRect.top);
        if (visible.length > 0) setActive(visible[0].target.id as SectionId);
      },
      { root, rootMargin: "0px 0px -70% 0px", threshold: 0 },
    );

    SECTIONS.forEach(({ id }) => {
      const el = document.getElementById(id);
      if (el) ioRef.current!.observe(el);
    });

    return () => ioRef.current?.disconnect();
  }, []);

  return (
    <SidebarGroup>
      <SidebarGroupLabel>Account</SidebarGroupLabel>
      <SidebarMenu role="navigation" aria-label="Settings navigation">
        {SECTIONS.map(({ id, label, icon: Icon }) => (
          <SidebarMenuItem key={id}>
            <SidebarMenuButton
              isActive={active === id}
              onClick={() => scrollTo(id)}
              aria-current={active === id ? "page" : undefined}
              aria-label={`Navigate to ${label} section`}
            >
              <Icon aria-hidden="true" />
              <span>{label}</span>
            </SidebarMenuButton>
          </SidebarMenuItem>
        ))}
      </SidebarMenu>
    </SidebarGroup>
  );
}
