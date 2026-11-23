import type { Metadata } from "next";
import { ModelLab } from "@/components/models/model-lab";

export const metadata: Metadata = { title: "Model Lab · Nexus City" };

export default function ModelsPage() {
  return <ModelLab />;
}
