import { Link } from "chen-the-dawnstreak";
import { Button } from "@heroui/react";

import { PageContainer } from "@/components/page/primitives";

export default function NotFound() {
  return (
    <PageContainer>
      <div className="mx-auto flex max-w-[520px] flex-col items-center py-16 text-center">
        <div className="relative mb-6 flex items-baseline justify-center font-mono text-[88px] font-bold leading-none text-fg/15">
          <span>4</span>
          <span
            aria-hidden
            className="mx-1 inline-block h-[64px] w-[64px] translate-y-2 rounded-full"
            style={{
              background:
                "conic-gradient(from 210deg, var(--accent), transparent 65%, var(--accent))",
              maskImage:
                "radial-gradient(circle, transparent 32%, black 33%, black 70%, transparent 71%)",
              WebkitMaskImage:
                "radial-gradient(circle, transparent 32%, black 33%, black 70%, transparent 71%)",
            }}
          />
          <span>4</span>
        </div>
        <h1 className="m-0 text-[20px] font-semibold text-fg">页面走丢了</h1>
        <p className="mt-2 max-w-[42ch] text-[13px] text-muted">
          这条路径不对应任何资源 — 可能 URL 拼错了，或者它已经被移除/重命名。
        </p>
        <div className="mt-6 flex items-center gap-2">
          <Link to="/orgs">
            <Button>回到组织首页</Button>
          </Link>
          <Link to="/settings/profile">
            <Button variant="outline">个人资料</Button>
          </Link>
        </div>
      </div>
    </PageContainer>
  );
}
