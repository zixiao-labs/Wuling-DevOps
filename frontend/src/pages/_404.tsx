import { Link } from "chen-the-dawnstreak";

export default function NotFound() {
  return (
    <div style={{ textAlign: "center", padding: "4rem 1rem", color: "var(--muted)" }}>
      <div style={{ fontSize: "4rem", fontWeight: 700, color: "var(--foreground)" }}>404</div>
      <p>找不到这个页面。</p>
      <Link to="/" style={{ color: "var(--accent)" }}>
        回到首页
      </Link>
    </div>
  );
}
