export function NotFound() {
  return (
    <div className="flex min-h-screen items-center justify-center bg-bg p-8 text-fg">
      <div className="text-center">
        <h1 className="text-2xl font-bold">404</h1>
        <p className="mt-2 text-fg/70">页面不存在</p>
        <a href="/help" className="mt-4 inline-block text-accent">
          返回帮助中心
        </a>
      </div>
    </div>
  );
}
