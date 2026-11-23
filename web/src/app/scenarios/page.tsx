import type { Metadata } from "next";
import { ScenarioLab } from "@/components/scenarios/scenario-lab";

export const metadata: Metadata = { title: "Scenario Lab · Nexus City" };

export default function ScenariosPage() {
  return <ScenarioLab />;
}
