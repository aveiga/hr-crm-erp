export function AuthShell({
  title,
  children,
}: {
  title: string;
  children: React.ReactNode;
}) {
  return (
    <div className="flex min-h-screen items-center justify-center px-4">
      <div className="card w-full max-w-md p-8">
        <div className="mb-6 text-center">
          <div className="text-lg font-semibold text-brand-700">HR · CRM · ERP</div>
          <h1 className="mt-2 text-xl font-bold">{title}</h1>
        </div>
        {children}
      </div>
    </div>
  );
}
