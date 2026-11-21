import type { Metadata } from "next";
import { TwinView } from "@/components/twin/twin-view";

export const metadata: Metadata = { title: "Live Twin · Nexus City" };

export default function TwinPage() {
  return <TwinView />;
}
