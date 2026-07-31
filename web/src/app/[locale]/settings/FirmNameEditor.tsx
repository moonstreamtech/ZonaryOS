"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";

type Props = {
  firmId: string;
  currentName: string;
};

// Item 4: firm name editing, wired into the settings page now that
// internal/firm.UpdateName exists to back it - the settings page was
// read-only until this batch because nothing existed to call (see
// page.tsx's prior doc comment, still true of every other firm field:
// this is name only, not a general firm-settings form). Owner-gated the
// same way components/Workflow/DefinitionBuilder.tsx's builder is:
// rendered only when the caller is an owner (checked by the server
// component that mounts this), tagged data-permission-public rather
// than data-permission-key - PATCH /api/firms/{firmId} is gated by the
// structural is_owner check (internal/permission.IsOwner), not a
// permission-catalog entry.
export default function FirmNameEditor({ firmId, currentName }: Props) {
  const t = useTranslations("Settings");
  const router = useRouter();

  const [editing, setEditing] = useState(false);
  const [name, setName] = useState(currentName);
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);

    if (name.trim() === "") {
      setError(t("firmNameRequired"));
      return;
    }

    setSubmitting(true);
    try {
      const res = await fetch("/api/firm/update-name", {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ firmId, name }),
      });
      if (!res.ok) {
        setError(
          res.status === 403 ? t("firmNamePermissionDenied") : t("firmNameError"),
        );
        return;
      }
      setEditing(false);
      router.refresh();
    } catch {
      setError(t("firmNameError"));
    } finally {
      setSubmitting(false);
    }
  }

  if (!editing) {
    return (
      <div className="flex items-center gap-2">
        <dd className="text-black dark:text-zinc-50">{currentName}</dd>
        <button
          type="button"
          data-permission-public="true"
          onClick={() => {
            setName(currentName);
            setEditing(true);
          }}
          className="text-xs font-medium text-zinc-700 underline-offset-4 hover:underline dark:text-zinc-300"
        >
          {t("firmNameEditButton")}
        </button>
      </div>
    );
  }

  return (
    <form onSubmit={handleSubmit} className="flex flex-col gap-2">
      {error && <p className="text-xs text-red-600 dark:text-red-400">{error}</p>}
      <div className="flex items-center gap-2">
        <input
          type="text"
          value={name}
          onChange={(e) => setName(e.target.value)}
          className="rounded-md border border-zinc-300 bg-white px-2 py-1 text-sm text-black dark:border-zinc-700 dark:bg-zinc-900 dark:text-white"
        />
        <button
          type="submit"
          data-permission-public="true"
          disabled={submitting}
          className="rounded-full bg-foreground px-3 py-1 text-xs font-medium text-background disabled:opacity-50 dark:hover:bg-[#ccc]"
        >
          {t("firmNameSaveButton")}
        </button>
        <button
          type="button"
          data-permission-public="true"
          onClick={() => setEditing(false)}
          className="rounded-full border border-zinc-300 px-3 py-1 text-xs font-medium text-zinc-700 dark:border-zinc-700 dark:text-zinc-300"
        >
          {t("firmNameCancelButton")}
        </button>
      </div>
    </form>
  );
}
