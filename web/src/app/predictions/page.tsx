import type { Metadata } from "next";
import { Predictions } from "@/components/predictions/predictions";

export const metadata: Metadata = { title: "AI Predictions · Nexus City" };

export default function PredictionsPage() {
  return <Predictions />;
}
