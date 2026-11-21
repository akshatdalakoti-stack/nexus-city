"use client";

import { useEffect, useRef } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { MotionConfig } from "motion/react";
import {
  type LucideIcon,
  BrainCircuit,
  FlaskConical,
  LayoutDashboard,
  Map as MapIcon,
  MessageSquareText,
  Microscope,
  ServerCog,
} from "lucide-react";
import { cn } from "@/lib/utils";
import { SimClockBar } from "./sim-clock-bar";

type NavItem = { href: string; label: string; icon: LucideIcon };

/** Navigation grouped the way ops tools do it (Linear, Vercel): what you watch, what you analyse, what runs it. */
const GROUPS: { label: string; items: NavItem[] }[] = [
  {
    label: "Monitor",
    items: [
      { href: "/", label: "Overview", icon: LayoutDashboard },
      { href: "/twin", label: "Live Twin", icon: MapIcon },
      { href: "/predictions", label: "AI Predictions", icon: BrainCircuit },
    ],
  },
  {
    label: "Analyze",
    items: [
      { href: "/scenarios", label: "Scenario Lab", icon: FlaskConical },
      { href: "/intelligence", label: "City Intelligence", icon: MessageSquareText },
      { href: "/models", label: "Model Lab", icon: Microscope },
    ],
  },
  {
    label: "Platform",
    items: [{ href: "/infrastructure", label: "Infrastructure", icon: ServerCog }],
  },
];

const NAV: NavItem[] = GROUPS.flatMap((g) => g.items);

export function pageTitle(pathname: string) {
  return NAV.find((n) => isActive(n.href, pathname))?.label ?? "Overview";
}

/** Monochrome mark: an N drawn as a route between two stops. */
function Logo() {
  return (
    <div className="flex items-center gap-2.5 px-2">
      <svg viewBox="0 0 28 28" className="size-7 shrink-0" aria-hidden>
        <rect width="28" height="28" rx="7" fill="#ececee" />
        <path d="M8.5 19.5v-11l11 11v-11" fill="none" stroke="#0a0a0b" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" />
      </svg>
      <div className="min-w-0 leading-tight">
        <div className="text-[13.5px] font-semibold text-foreground">Nexus City</div>
        <div className="text-[11.5px] text-muted-foreground">Digital twin</div>
      </div>
    </div>
  );
}

function isActive(href: string, pathname: string) {
  return href === "/" ? pathname === "/" : pathname.startsWith(href);
}

/** Below the md breakpoint the sidebar is hidden; this strip replaces it. */
function MobileNav({ pathname }: { pathname: string }) {
  const nav = useRef<HTMLElement>(null);
  // Keep the current page's tab in view in the scrolling strip.
  useEffect(() => {
    nav.current
      ?.querySelector<HTMLElement>('[aria-current="page"]')
      ?.scrollIntoView({ inline: "center", block: "nearest" });
  }, [pathname]);
  return (
    <nav
      ref={nav}
      aria-label="Main"
      className="flex shrink-0 gap-1 overflow-x-auto border-b border-border bg-sidebar px-2 py-1.5 md:hidden"
    >
      {NAV.map((item) => {
        const Icon = item.icon;
        const active = isActive(item.href, pathname);
        return (
          <Link
            key={item.label}
            href={item.href}
            aria-current={active ? "page" : undefined}
            className={cn(
              "flex shrink-0 items-center gap-1.5 rounded-md px-2.5 py-1.5 text-[13px]",
              active ? "bg-sidebar-accent text-foreground" : "text-sidebar-foreground",
            )}
          >
            <Icon className="size-3.5" aria-hidden />
            {item.label}
          </Link>
        );
      })}
    </nav>
  );
}

export function AppShell({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  return (
    // reducedMotion="user": Motion animations follow the OS "reduce motion" setting.
    <MotionConfig reducedMotion="user">
      <a
        href="#main"
        className="sr-only z-50 rounded-md bg-primary px-3 py-2 text-sm font-medium text-primary-foreground focus:not-sr-only focus:fixed focus:left-3 focus:top-3"
      >
        Skip to content
      </a>
      <div className="flex h-dvh overflow-hidden">
        <aside className="hidden w-[232px] shrink-0 flex-col border-r border-sidebar-border bg-sidebar px-3 pb-3 pt-4 md:flex">
          <Logo />
          <nav className="mt-6 flex flex-col gap-5" aria-label="Main">
            {GROUPS.map((g) => (
              <div key={g.label}>
                <div className="mb-1 px-2 text-[11.5px] font-medium text-muted-foreground">{g.label}</div>
                <div className="flex flex-col gap-px">
                  {g.items.map((item) => {
                    const Icon = item.icon;
                    const active = isActive(item.href, pathname);
                    return (
                      <Link
                        key={item.label}
                        href={item.href}
                        aria-current={active ? "page" : undefined}
                        className={cn(
                          "flex h-8 items-center gap-2.5 rounded-md px-2 text-[13.5px] transition-colors",
                          active
                            ? "bg-sidebar-accent font-medium text-foreground"
                            : "text-sidebar-foreground hover:bg-sidebar-accent/60 hover:text-foreground",
                        )}
                      >
                        <Icon className={cn("size-4", active ? "text-foreground" : "text-muted-foreground")} aria-hidden />
                        {item.label}
                      </Link>
                    );
                  })}
                </div>
              </div>
            ))}
          </nav>
          <div className="mt-auto rounded-lg border border-border px-3 py-2.5 text-[12px] leading-relaxed text-muted-foreground">
            <div className="mb-0.5 flex items-center gap-1.5 font-medium text-foreground">
              <span className="size-1.5 rounded-full bg-warn" aria-hidden />
              Synthetic data
            </div>
            Every value comes from the simulation, not a real city.
          </div>
        </aside>
        <div className="flex min-w-0 flex-1 flex-col">
          <SimClockBar title={pageTitle(pathname)} />
          <MobileNav pathname={pathname} />
          <main id="main" tabIndex={-1} className="min-h-0 flex-1 overflow-auto focus:outline-none">
            {children}
          </main>
        </div>
      </div>
    </MotionConfig>
  );
}
