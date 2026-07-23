import type { WorkItem, WorkItemState, WorkItemType } from "@/api/types";

export const WORK_ITEM_STATES: ReadonlyArray<{ id: WorkItemState; label: string }> = [
  { id: "new", label: "待办" },
  { id: "active", label: "进行中" },
  { id: "resolved", label: "待验收" },
  { id: "closed", label: "已完成" },
];

export const WORK_ITEM_TYPES: ReadonlyArray<{ id: WorkItemType; label: string }> = [
  { id: "epic", label: "Epic" },
  { id: "feature", label: "Feature" },
  { id: "user_story", label: "User Story" },
  { id: "task", label: "Task" },
  { id: "bug", label: "Bug" },
];

export function groupWorkItems(items: readonly WorkItem[]): Record<WorkItemState, WorkItem[]> {
  const grouped: Record<WorkItemState, WorkItem[]> = {
    new: [],
    active: [],
    resolved: [],
    closed: [],
  };
  for (const item of items) grouped[item.state].push(item);
  return grouped;
}

export function totalStoryPoints(items: readonly WorkItem[]): number {
  return items.reduce((sum, item) => sum + (item.story_points ?? 0), 0);
}

export function shortWorkItemID(prefix: string, number: number): string {
  const clean = prefix.trim().toUpperCase();
  return clean ? `${clean}-${number}` : `#${number}`;
}
