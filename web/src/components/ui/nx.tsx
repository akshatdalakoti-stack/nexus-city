"use client";

/**
 * Nexus command-center primitives, in the spirit of Linear and Vercel:
 * flat near-black surfaces, hairline borders, sentence-case labels, one
 * accent colour, and numbers set in tabular figures.
 */

import type { LucideIcon } from "lucide-react";
import { cn } from "@/lib/utils";

export function Page({ children, className, wide = false }: { children: React.ReactNode; className?: string; wide?: boolean }) {
  return <div className={cn("mx-auto w-full space-y-5 px-4 py-6 sm:px-6", wide ? "max-w-[1600px]" : "max-w-[1400px]", className)}>{children}</div>;
}

export function PageHeader({ title, description, actions, meta }: {
  title: string;
  description?: React.ReactNode;
  actions?: React.ReactNode;
  meta?: React.ReactNode;
}) {
  return (
    <header className="flex flex-wrap items-start gap-x-6 gap-y-3">
      <div className="min-w-0 flex-1">
        <h1 className="text-[22px] font-semibold tracking-[-0.01em] text-foreground">{title}</h1>
        {description && <p className="mt-1 max-w-3xl text-[13.5px] leading-relaxed text-muted-foreground">{description}</p>}
        {meta && <div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-[12.5px] text-muted-foreground">{meta}</div>}
      </div>
      {actions && <div className="flex shrink-0 flex-wrap items-center gap-2">{actions}</div>}
    </header>
  );
}

/** A card with an optional header row separated by a hairline (Linear/Vercel style). */
export function Card({ title, description, icon: Icon, action, children, className, bodyClassName, flush = false }: {
  title?: React.ReactNode;
  description?: React.ReactNode;
  icon?: LucideIcon;
  action?: React.ReactNode;
  children: React.ReactNode;
  className?: string;
  bodyClassName?: string;
  /** No body padding (tables, maps). */
  flush?: boolean;
}) {
  return (
    <section className={cn("panel flex min-w-0 flex-col rounded-xl", className)}>
      {(title || action) && (
        <header className="flex min-h-11 items-center gap-2 border-b border-border px-4 py-2.5">
          {Icon && <Icon className="size-4 shrink-0 text-muted-foreground" aria-hidden />}
          <div className="min-w-0 flex-1">
            {title && <h2 className="truncate text-[13.5px] font-medium text-foreground">{title}</h2>}
            {description && <p className="truncate text-[12px] text-muted-foreground">{description}</p>}
          </div>
          {action && <div className="flex shrink-0 items-center gap-2">{action}</div>}
        </header>
      )}
      <div className={cn("min-w-0 flex-1", !flush && "p-4", bodyClassName)}>{children}</div>
    </section>
  );
}

/** KPI tile: label, big tabular number, quiet subline, optional sparkline. */
export function Stat({ label, value, unit, sub, tone, children, className }: {
  label: React.ReactNode;
  value: React.ReactNode;
  unit?: string;
  sub?: React.ReactNode;
  tone?: "ok" | "warn" | "crit";
  children?: React.ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("panel relative flex min-w-0 flex-col overflow-hidden rounded-xl px-4 pb-3 pt-3.5", className)}>
      <div className="truncate text-[12.5px] font-medium text-muted-foreground">{label}</div>
      <div className={cn("mt-1.5 flex items-baseline gap-1 text-[26px] font-semibold leading-none tracking-[-0.02em] num",
        tone === "ok" && "text-ok", tone === "warn" && "text-warn", tone === "crit" && "text-crit")}>
        {value}
        {unit && <span className="text-[13px] font-medium tracking-normal text-muted-foreground">{unit}</span>}
      </div>
      {sub && <div className="mt-1.5 min-h-4 truncate text-[12px] text-muted-foreground">{sub}</div>}
      {children}
    </div>
  );
}

const BUTTON = {
  primary: "bg-primary text-primary-foreground hover:bg-primary/85",
  secondary: "border border-border bg-surface text-foreground hover:bg-accent",
  ghost: "text-muted-foreground hover:bg-accent hover:text-foreground",
} as const;

export function buttonClass(variant: keyof typeof BUTTON = "secondary", size: "sm" | "md" = "sm") {
  return cn(
    "inline-flex items-center justify-center gap-1.5 rounded-md font-medium transition-colors disabled:pointer-events-none disabled:opacity-40",
    size === "sm" ? "h-8 px-3 text-[13px]" : "h-9 px-4 text-[13.5px]",
    BUTTON[variant],
  );
}

export function Pill({ children, tone = "neutral", className }: {
  children: React.ReactNode;
  tone?: "neutral" | "brand" | "ok" | "warn" | "crit";
  className?: string;
}) {
  const tones = {
    neutral: "border-border bg-raised text-muted-foreground",
    brand: "border-brand/30 bg-brand-soft text-brand",
    ok: "border-ok/30 bg-ok/10 text-ok",
    warn: "border-warn/30 bg-warn/10 text-warn",
    crit: "border-crit/30 bg-crit/10 text-crit",
  };
  return (
    <span className={cn("inline-flex items-center gap-1.5 rounded-md border px-1.5 py-0.5 text-[12px] font-medium leading-4", tones[tone], className)}>
      {children}
    </span>
  );
}

export function Dot({ className, color }: { className?: string; color?: string }) {
  return <span className={cn("inline-block size-2 shrink-0 rounded-full", className)} style={color ? { background: color } : undefined} aria-hidden />;
}
