import { CheckIcon, ClipboardIcon } from "@heroicons/react/24/outline";
import type { ReactNode } from "react";
import React, { useState } from "react";

import { secondaryButton } from "./styles";

interface EmptyStateProps {
  title: string;
  children?: ReactNode;
}

export const EmptyState: React.FC<EmptyStateProps> = ({ title, children }) => (
  <div className="flex flex-col items-center gap-2 rounded-lg border border-dashed border-gray-300 bg-white/60 px-4 py-8 text-center">
    <p className="font-medium text-gray-700">{title}</p>
    {children !== undefined && (
      <div className="text-sm text-gray-500">{children}</div>
    )}
  </div>
);

interface PageHeaderProps {
  title: ReactNode;
  subtitle?: ReactNode;
  actions?: ReactNode;
}

export const PageHeader: React.FC<PageHeaderProps> = ({
  title,
  subtitle,
  actions,
}) => (
  <div className="mb-4 flex flex-wrap items-end justify-between gap-3">
    <div className="min-w-0">
      <h1 className="text-2xl font-bold text-gray-900">{title}</h1>
      {subtitle !== undefined && (
        <div className="mt-1 text-sm text-gray-600">{subtitle}</div>
      )}
    </div>
    {actions !== undefined && (
      <div className="flex flex-wrap items-center gap-2">{actions}</div>
    )}
  </div>
);

interface StatProps {
  label: string;
  value: ReactNode;
  sub?: ReactNode;
}

export const Stat: React.FC<StatProps> = ({ label, value, sub }) => (
  <div className="flex flex-col">
    <span className="text-xs font-medium tracking-wide text-gray-500 uppercase">
      {label}
    </span>
    <span className="text-lg font-semibold text-gray-900 tabular-nums">
      {value}
    </span>
    {sub !== undefined && <span className="text-xs text-gray-500">{sub}</span>}
  </div>
);

interface BadgeProps {
  children: ReactNode;
  className?: string;
  title?: string;
}

export const Badge: React.FC<BadgeProps> = ({
  children,
  className = "bg-gray-100 text-gray-700 ring-gray-300",
  title,
}) => (
  <span
    title={title}
    className={`inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium whitespace-nowrap ring-1 ring-inset ${className}`}
  >
    {children}
  </span>
);

interface ProgressBarProps {
  value: number;
  target: number;
  className?: string;
}

/**
 * ProgressBar shows value against target. Overshooting the target shows a
 * full bar in a darker color.
 */
export const ProgressBar: React.FC<ProgressBarProps> = ({
  value,
  target,
  className = "bg-brand-500",
}) => {
  if (target <= 0) {
    return null;
  }
  const fraction = Math.min(value / target, 1);
  const over = value > target * 1.1;
  return (
    <div className="h-1.5 w-full overflow-hidden rounded-full bg-gray-200">
      <div
        className={`h-full rounded-full ${over ? "bg-amber-500" : className}`}
        style={{ width: `${fraction * 100}%` }}
      />
    </div>
  );
};

interface CopyFieldProps {
  value: string;
  multiline?: boolean;
}

// CopyField shows a value in a monospace box with a copy button.
export const CopyField: React.FC<CopyFieldProps> = ({
  value,
  multiline = false,
}) => {
  const [copied, setCopied] = useState(false);
  const copy = () => {
    void navigator.clipboard.writeText(value).then(() => {
      setCopied(true);
      globalThis.setTimeout(() => {
        setCopied(false);
      }, 1500);
    });
  };
  /*
   * It shrinks to its container, scrolling a long value, rather than
   * widening a column or dialog to the value's width.
   */
  return (
    <div className="flex w-full min-w-0 items-start gap-2">
      {multiline ? (
        <pre className="min-w-0 grow overflow-x-auto rounded-md bg-gray-900 p-3 font-mono text-xs text-gray-100">
          {value}
        </pre>
      ) : (
        <code className="min-w-0 grow overflow-x-auto rounded-md bg-gray-100 px-2 py-1.5 font-mono text-xs whitespace-nowrap text-gray-900 ring-1 ring-gray-200">
          {value}
        </code>
      )}
      <button
        type="button"
        className={`${secondaryButton} shrink-0`}
        onClick={copy}
        aria-label="Copy to clipboard"
      >
        {copied ? (
          <CheckIcon className="size-4 text-emerald-700" />
        ) : (
          <ClipboardIcon className="size-4" />
        )}
        <span className="hidden sm:inline">{copied ? "Copied" : "Copy"}</span>
      </button>
    </div>
  );
};
