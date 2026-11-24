import type { Metadata } from "next";
import { IntelligenceChat } from "@/components/intelligence/intelligence-chat";

export const metadata: Metadata = { title: "City Intelligence · Nexus City" };

export default function IntelligencePage() {
  return <IntelligenceChat />;
}
