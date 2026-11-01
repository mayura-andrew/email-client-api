import { Campaign } from "@/lib/api";
import { SegmentedProgress } from "./ui";

/** Splits a campaign into delivered / failed / in-flight using the list counts. */
export function CampaignProgress({ campaign, height = 8 }: { campaign: Campaign; height?: number }) {
  const total = Math.max(campaign.validCount, campaign.sentCount + campaign.failedCount);
  const inFlight = campaign.status === "running" ? Math.max(0, total - campaign.sentCount - campaign.failedCount) : 0;
  return (
    <SegmentedProgress total={total} height={height} striped={campaign.status === "running"} segments={[
      { key: "sent", value: campaign.sentCount },
      { key: "failed", value: campaign.failedCount },
      { key: "queued", value: inFlight },
    ]} />
  );
}

export function percentDone(campaign: Campaign): number {
  const total = Math.max(campaign.validCount, 1);
  return Math.min(100, Math.round(((campaign.sentCount + campaign.failedCount) / total) * 100));
}
