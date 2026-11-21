import type { Metadata } from "next";
import { Geist, Geist_Mono } from "next/font/google";
import "maplibre-gl/dist/maplibre-gl.css";
import "./globals.css";
import { LiveProvider } from "@/components/live-provider";
import { AppShell } from "@/components/shell/app-shell";
import { TooltipProvider } from "@/components/ui/tooltip";

const geistSans = Geist({ variable: "--font-geist-sans", subsets: ["latin"] });
const geistMono = Geist_Mono({ variable: "--font-geist-mono", subsets: ["latin"] });

export const metadata: Metadata = {
  title: "Nexus City · Digital Twin",
  description: "Command center for the Nexus City smart-city digital twin (synthetic simulation).",
};

export default function RootLayout({ children }: LayoutProps<"/">) {
  return (
    <html lang="en" className={`dark ${geistSans.variable} ${geistMono.variable} h-full antialiased`}>
      <body className="h-full">
        <TooltipProvider>
          <LiveProvider>
            <AppShell>{children}</AppShell>
          </LiveProvider>
        </TooltipProvider>
      </body>
    </html>
  );
}
