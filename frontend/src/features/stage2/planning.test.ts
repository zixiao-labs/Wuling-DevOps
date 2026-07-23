import { describe, expect, test } from "@lightning-js/lightning";

import type { WorkItem } from "@/api/types";
import { groupWorkItems, shortWorkItemID, totalStoryPoints } from "./planning";

function item(number: number, state: WorkItem["state"], points?: number): WorkItem {
  return {
    id: String(number),
    project_id: "project",
    number,
    type: "task",
    title: `Item ${number}`,
    description: "",
    state,
    priority: 2,
    story_points: points,
    area_path: "",
    backlog_order: number * 1024,
    created_at: "2026-07-10T00:00:00Z",
    updated_at: "2026-07-10T00:00:00Z",
  };
}

describe("Stage 2 planning helpers", () => {
  test("groups every work item into stable Scrum columns", () => {
    const source = [item(1, "active"), item(2, "new"), item(3, "active"), item(4, "closed")];
    const grouped = groupWorkItems(source);

    expect(Object.keys(grouped)).toEqual(["new", "active", "resolved", "closed"]);
    expect(grouped.new.map((value) => value.number)).toEqual([2]);
    expect(grouped.active.map((value) => value.number)).toEqual([1, 3]);
    expect(grouped.resolved).toEqual([]);
    expect(grouped.closed.map((value) => value.number)).toEqual([4]);
    expect(source.map((value) => value.number)).toEqual([1, 2, 3, 4]);
  });

  test("adds only estimated story points", () => {
    expect(totalStoryPoints([item(1, "new", 3), item(2, "active"), item(3, "closed", 5.5)])).toBe(8.5);
  });

  const idCases: Array<[string, number, string]> = [
    ["wl", 42, "WL-42"],
    ["  dev  ", 7, "DEV-7"],
    ["", 12, "#12"],
  ];

  test.each(idCases)("formats the configured work item prefix", (prefix, number, expected) => {
    expect(shortWorkItemID(prefix, number)).toBe(expected);
  });
});
