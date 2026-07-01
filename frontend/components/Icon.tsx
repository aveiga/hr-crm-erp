"use client";

import { icons } from "lucide-react";

function toPascal(name: string): string {
  return name
    .split("-")
    .map((s) => s.charAt(0).toUpperCase() + s.slice(1))
    .join("");
}

export function Icon({
  name,
  className,
}: {
  name?: string;
  className?: string;
}) {
  const key = toPascal(name || "circle") as keyof typeof icons;
  const Cmp = (icons[key] || icons.Circle) as React.ComponentType<{
    className?: string;
  }>;
  return <Cmp className={className} />;
}
