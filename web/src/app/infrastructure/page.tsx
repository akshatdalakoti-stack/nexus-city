import type { Metadata } from "next";
import { InfrastructureView } from "@/components/infrastructure/infrastructure-view";

export const metadata: Metadata = { title: "Infrastructure · Nexus City" };

export default function InfrastructurePage() {
  return <InfrastructureView />;
}
