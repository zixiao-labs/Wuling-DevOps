import { describe, expect, test } from "@lightning-js/lightning";

import { parseGitHubRepository } from "./github-link";

describe("parseGitHubRepository", () => {
  test.each([
    ["zixiao-labs/Wuling-DevOps", { owner: "zixiao-labs", name: "Wuling-DevOps" }],
    ["https://github.com/zixiao-labs/Wuling-DevOps", { owner: "zixiao-labs", name: "Wuling-DevOps" }],
    ["https://github.com/zixiao-labs/Wuling-DevOps.git", { owner: "zixiao-labs", name: "Wuling-DevOps" }],
    ["git@github.com:zixiao-labs/Wuling-DevOps.git", { owner: "zixiao-labs", name: "Wuling-DevOps" }],
  ])("parses %s", (value, expected) => {
    expect(parseGitHubRepository(value as string)).toEqual(expected);
  });

  test.each(["", "only-owner", "https://gitlab.com/acme/app", "acme/app/extra"])(
    "rejects %s",
    (value) => {
      expect(parseGitHubRepository(value)).toBe(null);
    },
  );
});
