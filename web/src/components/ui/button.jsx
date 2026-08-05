import { cn } from "../../lib/utils";

const buttonClassName =
  "inline-flex items-center justify-center whitespace-nowrap bg-delta-accent font-mono text-xs text-[var(--acc-contrast)] transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-delta-accent disabled:pointer-events-none disabled:opacity-50";

export function Button({ className, ...props }) {
  return <button className={cn(buttonClassName, className)} {...props} />;
}
