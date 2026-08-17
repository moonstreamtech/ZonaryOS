"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import type { Account } from "@/lib/accounting";
import type { DefinitionSpecInput } from "@/lib/workflow";
import type { AIWorkflowSuggestion } from "@/lib/aiConfig";
import {
  validateBuilderSpec,
  type BuilderFieldRow,
  type BuilderJournalLineRow,
  type BuilderStateRow,
  type BuilderTransitionRow,
  type BuilderValidationError,
  type FieldTypeValue,
} from "./builderValidation";

type Props = {
  firmId: string;
  // existingDefinitionKeys is Open Points item 38's reference-field
  // target list: the firm's OTHER already-defined workflow definitions
  // (see app/[locale]/workflows/page.tsx's own fetchDefinitions call,
  // reused here rather than a second fetch) - a "reference" field picks
  // its target from this real list, never free text, matching
  // internal/workflow.DefineWorkflowTx's own existence check for the same
  // key server-side. Empty (a firm with no other definitions yet, or an
  // owner defining their firm's very first workflow) simply means the
  // reference field type has nothing to offer yet - see its own
  // rendering below.
  existingDefinitionKeys: string[];
  // accounts is the firm's real chart of accounts (internal/accounting.ListAccounts,
  // fetched once by app/[locale]/workflows/page.tsx and handed down here,
  // same convention as existingDefinitionKeys above) - a journal
  // template's account-code picker below chooses from this real list,
  // never free text, matching internal/accounting.PostJournalEntryTx's
  // own account-code lookup server-side. Empty (a firm with no accounts
  // yet, which shouldn't normally happen since the wizard always seeds a
  // core chart) simply means the journal editor has nothing to offer -
  // the expander itself still opens, but with an empty dropdown.
  accounts: Account[];
};

let nextRowId = 0;
function emptyState(): BuilderStateRow {
  return { id: nextRowId++, key: "", name: "", isInitial: false, isTerminal: false };
}
function emptyTransition(): BuilderTransitionRow {
  return {
    id: nextRowId++,
    fromStateKey: "",
    toStateKey: "",
    actionKey: "",
    name: "",
    permissionKey: "",
    permissionDescription: "",
    journalEnabled: false,
    journalDescription: "",
    journalLines: [],
  };
}
function emptyJournalLine(): BuilderJournalLineRow {
  return { id: nextRowId++, accountCode: "", side: "debit", amountField: "" };
}
function emptyField(): BuilderFieldRow {
  return {
    id: nextRowId++,
    name: "",
    type: "string",
    required: false,
    options: [],
    referenceDefinitionKey: "",
    arrayItemType: "string",
  };
}

// Item 1's frontend half: lets a firm's owner actually use the workflow
// engine's own definition mechanism (internal/workflow.DefineWorkflow,
// exposed over HTTP as DefineWorkflowForFirm) instead of it only ever
// being reachable from Go fixtures/the firm-creation wizard. Deliberately
// mechanical - a form for entering states and transitions - not a visual/
// drag-and-drop graph editor, matching CreateInstanceForm.tsx's
// same "structural form, not a bigger UI project" scope decision.
//
// This is strictly the state-machine shape (states, transitions,
// permission keys) - it has no concept of conditional logic/rules
// (Open Points item 12's Rule Engine remains undesigned and untouched).
//
// Owner-gated the same way components/AuditMode/AuditModeClient.tsx's
// own toggle is: rendered only when the caller is an owner (checked by
// the server component that mounts this), tagged data-permission-public
// rather than data-permission-key - this isn't gated by the permission
// catalog (internal/permission.Has), it's the structural is_owner check
// DefineWorkflowForFirm itself enforces, same category as Permission
// Audit Mode's own toggle.
export default function DefinitionBuilder({ firmId, existingDefinitionKeys, accounts }: Props) {
  const t = useTranslations("Workflow");
  const router = useRouter();

  const [open, setOpen] = useState(false);
  const [key, setKey] = useState("");
  const [name, setName] = useState("");
  const [createPermissionKey, setCreatePermissionKey] = useState("");
  const [createPermissionDescription, setCreatePermissionDescription] = useState("");
  const [states, setStates] = useState<BuilderStateRow[]>([emptyState()]);
  const [transitions, setTransitions] = useState<BuilderTransitionRow[]>([]);
  // Starts empty - Open Points item 35's schema step is genuinely
  // optional: an owner who never clicks "add payload field" submits
  // exactly the same request this form always sent, no new required
  // section in their way.
  const [fields, setFields] = useState<BuilderFieldRow[]>([]);
  const [errors, setErrors] = useState<BuilderValidationError[]>([]);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  // "Suggest with AI" (Vision §5): a plain-language description goes to
  // internal/ai.SuggestWorkflow, and a successful response pre-fills this
  // form's OWN state setters below (applyAiSuggestion) - the same fields
  // an owner would have typed by hand. Nothing is ever submitted on the
  // owner's behalf (Never-Violate Rule 9): applying a suggestion only
  // populates the form, "Create workflow" still requires its own click.
  // Owner-gated on the backend (same tier as this whole builder), so this
  // panel is tagged data-permission-public the same way the rest of the
  // form is.
  const [aiPanelOpen, setAiPanelOpen] = useState(false);
  const [aiDescription, setAiDescription] = useState("");
  const [aiSubmitting, setAiSubmitting] = useState(false);
  const [aiResult, setAiResult] = useState<
    { kind: "notConfigured" } | { kind: "invalid"; raw: string } | { kind: "error"; message: string } | null
  >(null);

  const VALID_FIELD_TYPES = new Set<FieldTypeValue>([
    "string",
    "number",
    "boolean",
    "date",
    "enum",
    "reference",
    "array",
  ]);

  function applyAiSuggestion(s: AIWorkflowSuggestion) {
    setKey(s.key);
    setName(s.name);
    setCreatePermissionKey(s.createPermission.key);
    setCreatePermissionDescription(s.createPermission.description);
    setStates(
      s.states.length > 0
        ? s.states.map((st) => ({
            ...emptyState(),
            key: st.key,
            name: st.name,
            isInitial: st.isInitial,
            isTerminal: st.isTerminal,
          }))
        : [emptyState()],
    );
    setTransitions(
      s.transitions.map((tr) => ({
        ...emptyTransition(),
        fromStateKey: tr.fromStateKey,
        toStateKey: tr.toStateKey,
        actionKey: tr.actionKey,
        name: tr.name,
        permissionKey: tr.permission.key,
        permissionDescription: tr.permission.description,
      })),
    );
    setFields(
      (s.fields ?? []).map((f) => ({
        ...emptyField(),
        name: f.name,
        type: VALID_FIELD_TYPES.has(f.type as FieldTypeValue) ? (f.type as FieldTypeValue) : "string",
        required: f.required,
      })),
    );
    setAiPanelOpen(false);
    setAiDescription("");
    setAiResult(null);
  }

  async function handleAiSuggest(e: FormEvent) {
    e.preventDefault();
    setAiResult(null);
    if (!aiDescription.trim()) return;

    setAiSubmitting(true);
    try {
      const res = await fetch(`/api/ai/suggest-workflow/${firmId}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ description: aiDescription.trim() }),
      });
      if (res.status === 404) {
        setAiResult({ kind: "notConfigured" });
        return;
      }
      if (res.status === 422) {
        setAiResult({ kind: "invalid", raw: await res.text() });
        return;
      }
      if (!res.ok) {
        setAiResult({ kind: "error", message: t("aiSuggestError") });
        return;
      }
      applyAiSuggestion((await res.json()) as AIWorkflowSuggestion);
    } catch {
      setAiResult({ kind: "error", message: t("aiSuggestError") });
    } finally {
      setAiSubmitting(false);
    }
  }

  function updateState(id: number, patch: Partial<BuilderStateRow>) {
    setStates((prev) => prev.map((s) => (s.id === id ? { ...s, ...patch } : s)));
  }
  function removeState(id: number) {
    setStates((prev) => (prev.length === 1 ? prev : prev.filter((s) => s.id !== id)));
  }
  function updateTransition(id: number, patch: Partial<BuilderTransitionRow>) {
    setTransitions((prev) =>
      prev.map((tr) => (tr.id === id ? { ...tr, ...patch } : tr)),
    );
  }
  function removeTransition(id: number) {
    setTransitions((prev) => prev.filter((tr) => tr.id !== id));
  }
  function updateField(id: number, patch: Partial<BuilderFieldRow>) {
    setFields((prev) => prev.map((f) => (f.id === id ? { ...f, ...patch } : f)));
  }
  function removeField(id: number) {
    setFields((prev) => prev.filter((f) => f.id !== id));
  }

  function toggleJournal(transitionId: number) {
    setTransitions((prev) =>
      prev.map((tr) =>
        tr.id === transitionId
          ? {
              ...tr,
              journalEnabled: !tr.journalEnabled,
              // Opening the expander for the first time seeds two empty
              // lines - the minimum a balanced entry needs (Never-Violate:
              // an entry can never balance with fewer than two legs) -
              // rather than making the owner click "add line" twice
              // before anything useful can be entered.
              journalLines:
                !tr.journalEnabled && tr.journalLines.length === 0
                  ? [emptyJournalLine(), emptyJournalLine()]
                  : tr.journalLines,
            }
          : tr,
      ),
    );
  }
  function updateJournalLine(transitionId: number, lineId: number, patch: Partial<BuilderJournalLineRow>) {
    setTransitions((prev) =>
      prev.map((tr) =>
        tr.id === transitionId
          ? { ...tr, journalLines: tr.journalLines.map((l) => (l.id === lineId ? { ...l, ...patch } : l)) }
          : tr,
      ),
    );
  }
  function addJournalLine(transitionId: number) {
    setTransitions((prev) =>
      prev.map((tr) =>
        tr.id === transitionId ? { ...tr, journalLines: [...tr.journalLines, emptyJournalLine()] } : tr,
      ),
    );
  }
  function removeJournalLine(transitionId: number, lineId: number) {
    setTransitions((prev) =>
      prev.map((tr) =>
        tr.id === transitionId
          ? { ...tr, journalLines: tr.journalLines.filter((l) => l.id !== lineId) }
          : tr,
      ),
    );
  }

  function errorMessage(error: BuilderValidationError): string {
    switch (error.code) {
      case "keyRequired":
        return t("builderErrorKeyRequired");
      case "nameRequired":
        return t("builderErrorNameRequired");
      case "createPermissionKeyRequired":
        return t("builderErrorCreatePermissionKeyRequired");
      case "noStates":
        return t("builderErrorNoStates");
      case "stateKeyRequired":
        return t("builderErrorStateKeyRequired");
      case "duplicateStateKey":
        return t("builderErrorDuplicateStateKey", { key: error.key });
      case "notExactlyOneInitialState":
        return t("builderErrorNotExactlyOneInitialState", { count: error.count });
      case "transitionActionKeyRequired":
        return t("builderErrorTransitionActionKeyRequired");
      case "transitionPermissionKeyRequired":
        return t("builderErrorTransitionPermissionKeyRequired");
      case "unknownFromState":
        return t("builderErrorUnknownFromState", { key: error.key || "" });
      case "unknownToState":
        return t("builderErrorUnknownToState", { key: error.key || "" });
      case "duplicateTransition":
        return t("builderErrorDuplicateTransition", {
          fromStateKey: error.fromStateKey,
          actionKey: error.actionKey,
        });
      case "fieldNameRequired":
        return t("builderErrorFieldNameRequired");
      case "duplicateFieldName":
        return t("builderErrorDuplicateFieldName", { name: error.name });
      case "enumOptionsRequired":
        return t("builderErrorEnumOptionsRequired", { name: error.name });
      case "enumOptionEmpty":
        return t("builderErrorEnumOptionEmpty", { name: error.name });
      case "enumOptionDuplicate":
        return t("builderErrorEnumOptionDuplicate", { name: error.name, option: error.option });
      case "referenceDefinitionKeyRequired":
        return t("builderErrorReferenceDefinitionKeyRequired", { name: error.name });
      case "arrayItemTypeRequired":
        return t("builderErrorArrayItemTypeRequired", { name: error.name });
      case "journalDescriptionRequired":
        return t("builderErrorJournalDescriptionRequired", { actionKey: error.actionKey });
      case "journalNeedsTwoLines":
        return t("builderErrorJournalNeedsTwoLines", { actionKey: error.actionKey });
      case "journalLineAccountCodeRequired":
        return t("builderErrorJournalLineAccountCodeRequired", { actionKey: error.actionKey });
      case "journalLineAmountFieldRequired":
        return t("builderErrorJournalLineAmountFieldRequired", { actionKey: error.actionKey });
    }
  }

  function addFieldOption(fieldId: number) {
    setFields((prev) =>
      prev.map((f) => (f.id === fieldId ? { ...f, options: [...f.options, ""] } : f)),
    );
  }
  function updateFieldOption(fieldId: number, index: number, text: string) {
    setFields((prev) =>
      prev.map((f) =>
        f.id === fieldId
          ? { ...f, options: f.options.map((o, i) => (i === index ? text : o)) }
          : f,
      ),
    );
  }
  function removeFieldOption(fieldId: number, index: number) {
    setFields((prev) =>
      prev.map((f) =>
        f.id === fieldId ? { ...f, options: f.options.filter((_, i) => i !== index) } : f,
      ),
    );
  }

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setSubmitError(null);

    const spec = {
      key,
      name,
      createPermissionKey,
      createPermissionDescription,
      states,
      transitions,
      fields,
    };
    const validationErrors = validateBuilderSpec(spec);
    setErrors(validationErrors);
    if (validationErrors.length > 0) return;

    const wireSpec: DefinitionSpecInput = {
      key,
      name,
      createPermission: { key: createPermissionKey, description: createPermissionDescription },
      states: states.map((s) => ({
        key: s.key,
        name: s.name,
        isInitial: s.isInitial,
        isTerminal: s.isTerminal,
      })),
      transitions: transitions.map((tr) => ({
        fromStateKey: tr.fromStateKey,
        toStateKey: tr.toStateKey,
        actionKey: tr.actionKey,
        name: tr.name,
        permission: { key: tr.permissionKey, description: tr.permissionDescription },
        // Omitted entirely when the expander was never enabled, mirroring
        // `fields` below and TransitionSpec.Journal's own nil/omitted
        // means "post nothing" contract server-side.
        ...(tr.journalEnabled
          ? {
              journal: {
                description: tr.journalDescription,
                lines: tr.journalLines.map((l) => ({
                  accountCode: l.accountCode,
                  side: l.side,
                  amountField: l.amountField,
                })),
              },
            }
          : {}),
      })),
      // Omitted entirely when empty (rather than sent as `[]`) so a
      // definition built with no payload fields round-trips as "no
      // schema" the same way DefinitionSpec.Fields' own nil/empty
      // contract works Go-side - see lib/workflow.ts's DefinitionSpecInput.
      // Open Points item 38: options/referenceDefinitionKey/arrayItemType
      // are only sent for the field type they're actually meaningful for
      // - the backend's own DefinitionSpec.Validate rejects any of them
      // being set on the "wrong" type (spec.go's validateFieldShape), so
      // this mirrors that constraint on the way out rather than sending
      // every field's now-unused leftover builder state (e.g. a field
      // switched from "enum" back to "string" still has stale options in
      // BuilderFieldRow's own state, but that's a UI-state convenience,
      // not part of the submitted spec).
      ...(fields.length > 0
        ? {
            fields: fields.map((f) => ({
              name: f.name,
              type: f.type,
              required: f.required,
              ...(f.type === "enum" ? { options: f.options } : {}),
              ...(f.type === "reference" ? { referenceDefinitionKey: f.referenceDefinitionKey } : {}),
              ...(f.type === "array" ? { arrayItemType: f.arrayItemType } : {}),
            })),
          }
        : {}),
    };

    setSubmitting(true);
    try {
      const res = await fetch("/api/workflow/definitions", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ firmId, spec: wireSpec }),
      });
      if (!res.ok) {
        setSubmitError(
          res.status === 403
            ? t("builderPermissionDenied")
            : res.status === 409
              ? t("builderKeyExists")
              : t("builderSubmitError"),
        );
        return;
      }
      setOpen(false);
      setKey("");
      setName("");
      setCreatePermissionKey("");
      setCreatePermissionDescription("");
      setStates([emptyState()]);
      setTransitions([]);
      setFields([]);
      setErrors([]);
      router.refresh();
    } catch {
      setSubmitError(t("builderSubmitError"));
    } finally {
      setSubmitting(false);
    }
  }

  if (!open) {
    return (
      <button
        type="button"
        data-permission-public="true"
        onClick={() => setOpen(true)}
        className="rounded-full bg-foreground px-4 py-1.5 text-xs font-medium text-background transition-colors hover:bg-[#383838] dark:hover:bg-[#ccc]"
      >
        {t("builderOpenButton")}
      </button>
    );
  }

  return (
    <form
      onSubmit={handleSubmit}
      data-permission-public="true"
      className="flex w-full flex-col gap-4 rounded-lg border border-zinc-200 bg-white p-4 text-left dark:border-zinc-800 dark:bg-zinc-950"
    >
      <h2 className="text-sm font-medium text-zinc-700 dark:text-zinc-300">
        {t("builderTitle")}
      </h2>

      <div className="flex flex-col gap-2">
        <button
          type="button"
          data-permission-public="true"
          onClick={() => {
            setAiPanelOpen((v) => !v);
            setAiResult(null);
          }}
          className="self-start rounded-md border border-zinc-300 px-3 py-1.5 text-xs font-medium text-black hover:bg-zinc-100 dark:border-zinc-700 dark:text-zinc-50 dark:hover:bg-zinc-900"
        >
          {aiPanelOpen ? t("aiSuggestCancel") : t("aiSuggestOpenButton")}
        </button>

        {aiPanelOpen && (
          <form
            onSubmit={handleAiSuggest}
            data-permission-public="true"
            className="flex flex-col gap-2 rounded-md border border-zinc-300 bg-zinc-50 p-3 dark:border-zinc-700 dark:bg-zinc-900"
          >
            <label className="flex flex-col gap-1 text-xs text-zinc-600 dark:text-zinc-400">
              {t("aiSuggestLabel")}
              <textarea
                value={aiDescription}
                onChange={(e) => setAiDescription(e.target.value)}
                placeholder={t("aiSuggestPlaceholder")}
                rows={3}
                className={inputClass}
              />
            </label>

            {aiResult?.kind === "notConfigured" && (
              <p className="text-xs text-amber-700 dark:text-amber-400">{t("aiNotConfigured")}</p>
            )}
            {aiResult?.kind === "invalid" && (
              <div className="flex flex-col gap-1 text-xs text-red-600 dark:text-red-400">
                <p>{t("aiInvalidSuggestion")}</p>
                <pre className="overflow-x-auto rounded-md border border-red-300 bg-white p-2 text-[11px] whitespace-pre-wrap text-black dark:border-red-900 dark:bg-black dark:text-zinc-100">
                  {aiResult.raw}
                </pre>
              </div>
            )}
            {aiResult?.kind === "error" && (
              <p className="text-xs text-red-600 dark:text-red-400">{aiResult.message}</p>
            )}

            <button
              type="submit"
              data-permission-public="true"
              disabled={aiSubmitting || !aiDescription.trim()}
              className="self-start rounded-md bg-foreground px-3 py-1.5 text-xs font-medium text-background disabled:opacity-50"
            >
              {aiSubmitting ? t("aiSuggestSubmitting") : t("aiSuggestSubmit")}
            </button>
          </form>
        )}
      </div>

      {errors.length > 0 && (
        <ul className="list-disc pl-5 text-sm text-red-600 dark:text-red-400">
          {errors.map((err, i) => (
            <li key={i}>{errorMessage(err)}</li>
          ))}
        </ul>
      )}
      {submitError && (
        <p className="text-sm text-red-600 dark:text-red-400">{submitError}</p>
      )}

      <div className="flex flex-wrap gap-3">
        <Field label={t("builderKeyLabel")}>
          <input
            type="text"
            value={key}
            onChange={(e) => setKey(e.target.value)}
            placeholder={t("builderKeyPlaceholder")}
            className={inputClass}
          />
        </Field>
        <Field label={t("builderNameLabel")}>
          <input
            type="text"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder={t("builderNamePlaceholder")}
            className={inputClass}
          />
        </Field>
        <Field label={t("builderCreatePermissionKeyLabel")}>
          <input
            type="text"
            value={createPermissionKey}
            onChange={(e) => setCreatePermissionKey(e.target.value)}
            placeholder={t("builderPermissionKeyPlaceholder")}
            className={inputClass}
          />
        </Field>
        <Field label={t("builderCreatePermissionDescriptionLabel")}>
          <input
            type="text"
            value={createPermissionDescription}
            onChange={(e) => setCreatePermissionDescription(e.target.value)}
            className={inputClass}
          />
        </Field>
      </div>

      <div className="flex flex-col gap-2">
        <h3 className="text-xs font-medium text-zinc-600 dark:text-zinc-400">
          {t("builderStatesTitle")}
        </h3>
        {states.map((s) => (
          <div key={s.id} className="flex flex-wrap items-end gap-2">
            <Field label={t("builderStateKeyLabel")}>
              <input
                type="text"
                value={s.key}
                onChange={(e) => updateState(s.id, { key: e.target.value })}
                className={inputClass}
              />
            </Field>
            <Field label={t("builderStateNameLabel")}>
              <input
                type="text"
                value={s.name}
                onChange={(e) => updateState(s.id, { name: e.target.value })}
                className={inputClass}
              />
            </Field>
            <label className="flex items-center gap-1 pb-1.5 text-xs text-zinc-600 dark:text-zinc-400">
              <input
                type="checkbox"
                checked={s.isInitial}
                onChange={(e) => updateState(s.id, { isInitial: e.target.checked })}
              />
              {t("builderStateIsInitial")}
            </label>
            <label className="flex items-center gap-1 pb-1.5 text-xs text-zinc-600 dark:text-zinc-400">
              <input
                type="checkbox"
                checked={s.isTerminal}
                onChange={(e) => updateState(s.id, { isTerminal: e.target.checked })}
              />
              {t("builderStateIsTerminal")}
            </label>
            <button
              type="button"
              data-permission-public="true"
              disabled={states.length === 1}
              onClick={() => removeState(s.id)}
              className="rounded-md border border-zinc-300 px-2 py-1.5 text-xs text-zinc-600 disabled:opacity-30 dark:border-zinc-700 dark:text-zinc-400"
            >
              {t("removeFieldButton")}
            </button>
          </div>
        ))}
        <button
          type="button"
          data-permission-public="true"
          onClick={() => setStates((prev) => [...prev, emptyState()])}
          className="self-start text-xs font-medium text-zinc-700 underline-offset-4 hover:underline dark:text-zinc-300"
        >
          {t("builderAddStateButton")}
        </button>
      </div>

      <div className="flex flex-col gap-2">
        <h3 className="text-xs font-medium text-zinc-600 dark:text-zinc-400">
          {t("builderTransitionsTitle")}
        </h3>
        {transitions.map((tr) => (
          <div key={tr.id} className="flex flex-wrap items-end gap-2 border-t border-zinc-100 pt-2 dark:border-zinc-900">
            <Field label={t("builderTransitionFromStateLabel")}>
              <input
                type="text"
                value={tr.fromStateKey}
                onChange={(e) => updateTransition(tr.id, { fromStateKey: e.target.value })}
                className={inputClass}
              />
            </Field>
            <Field label={t("builderTransitionToStateLabel")}>
              <input
                type="text"
                value={tr.toStateKey}
                onChange={(e) => updateTransition(tr.id, { toStateKey: e.target.value })}
                className={inputClass}
              />
            </Field>
            <Field label={t("builderTransitionActionKeyLabel")}>
              <input
                type="text"
                value={tr.actionKey}
                onChange={(e) => updateTransition(tr.id, { actionKey: e.target.value })}
                className={inputClass}
              />
            </Field>
            <Field label={t("builderTransitionNameLabel")}>
              <input
                type="text"
                value={tr.name}
                onChange={(e) => updateTransition(tr.id, { name: e.target.value })}
                className={inputClass}
              />
            </Field>
            <Field label={t("builderTransitionPermissionKeyLabel")}>
              <input
                type="text"
                value={tr.permissionKey}
                onChange={(e) => updateTransition(tr.id, { permissionKey: e.target.value })}
                className={inputClass}
              />
            </Field>
            <button
              type="button"
              data-permission-public="true"
              onClick={() => removeTransition(tr.id)}
              className="rounded-md border border-zinc-300 px-2 py-1.5 text-xs text-zinc-600 dark:border-zinc-700 dark:text-zinc-400"
            >
              {t("removeFieldButton")}
            </button>

            {/* Financial management core: an optional journal template
                per transition (internal/workflow/spec.go's
                TransitionSpec.Journal) - the workflow-to-ledger bridge.
                Collapsed by default, matching the payload-fields
                section's own "optional, additive" presentation below. */}
            <div className="w-full pl-2">
              <button
                type="button"
                data-permission-public="true"
                onClick={() => toggleJournal(tr.id)}
                className="text-xs font-medium text-zinc-700 underline-offset-4 hover:underline dark:text-zinc-300"
              >
                {tr.journalEnabled ? t("builderJournalCollapse") : t("builderJournalExpand")}
              </button>

              {tr.journalEnabled && (
                <div className="mt-2 flex flex-col gap-2 border-l-2 border-zinc-200 pl-3 dark:border-zinc-800">
                  <Field label={t("builderJournalDescriptionLabel")}>
                    <input
                      type="text"
                      value={tr.journalDescription}
                      onChange={(e) =>
                        updateTransition(tr.id, { journalDescription: e.target.value })
                      }
                      placeholder={t("builderJournalDescriptionPlaceholder")}
                      className={inputClass}
                    />
                  </Field>

                  {tr.journalLines.map((line) => (
                    <div key={line.id} className="flex flex-wrap items-end gap-2">
                      <Field label={t("builderJournalAccountLabel")}>
                        {accounts.length === 0 ? (
                          <p className="text-xs text-zinc-500 dark:text-zinc-500">
                            {t("builderJournalNoAccounts")}
                          </p>
                        ) : (
                          <select
                            value={line.accountCode}
                            onChange={(e) =>
                              updateJournalLine(tr.id, line.id, { accountCode: e.target.value })
                            }
                            className={inputClass}
                          >
                            <option value="">{t("builderFieldReferenceUnselected")}</option>
                            {accounts.map((a) => (
                              <option key={a.id} value={a.code}>
                                {a.code} — {a.name}
                              </option>
                            ))}
                          </select>
                        )}
                      </Field>
                      <Field label={t("builderJournalSideLabel")}>
                        <select
                          value={line.side}
                          onChange={(e) =>
                            updateJournalLine(tr.id, line.id, {
                              side: e.target.value as BuilderJournalLineRow["side"],
                            })
                          }
                          className={inputClass}
                        >
                          <option value="debit">{t("builderJournalSideDebit")}</option>
                          <option value="credit">{t("builderJournalSideCredit")}</option>
                        </select>
                      </Field>
                      <Field label={t("builderJournalAmountFieldLabel")}>
                        <input
                          type="text"
                          value={line.amountField}
                          onChange={(e) =>
                            updateJournalLine(tr.id, line.id, { amountField: e.target.value })
                          }
                          placeholder={t("builderJournalAmountFieldPlaceholder")}
                          className={inputClass}
                        />
                      </Field>
                      <button
                        type="button"
                        data-permission-public="true"
                        onClick={() => removeJournalLine(tr.id, line.id)}
                        className="rounded-md border border-zinc-300 px-2 py-1.5 text-xs text-zinc-600 dark:border-zinc-700 dark:text-zinc-400"
                      >
                        {t("removeFieldButton")}
                      </button>
                    </div>
                  ))}
                  <button
                    type="button"
                    data-permission-public="true"
                    onClick={() => addJournalLine(tr.id)}
                    className="self-start text-xs font-medium text-zinc-700 underline-offset-4 hover:underline dark:text-zinc-300"
                  >
                    {t("builderJournalAddLineButton")}
                  </button>
                </div>
              )}
            </div>
          </div>
        ))}
        <button
          type="button"
          data-permission-public="true"
          onClick={() => setTransitions((prev) => [...prev, emptyTransition()])}
          className="self-start text-xs font-medium text-zinc-700 underline-offset-4 hover:underline dark:text-zinc-300"
        >
          {t("builderAddTransitionButton")}
        </button>
      </div>

      <div className="flex flex-col gap-2">
        <h3 className="text-xs font-medium text-zinc-600 dark:text-zinc-400">
          {t("builderFieldsTitle")}
        </h3>
        <p className="text-xs text-zinc-500 dark:text-zinc-500">{t("builderFieldsHint")}</p>
        {fields.map((f) => (
          <div key={f.id} className="flex flex-col gap-2 border-t border-zinc-100 pt-2 dark:border-zinc-900">
            <div className="flex flex-wrap items-end gap-2">
              <Field label={t("builderFieldNameLabel")}>
                <input
                  type="text"
                  value={f.name}
                  onChange={(e) => updateField(f.id, { name: e.target.value })}
                  className={inputClass}
                />
              </Field>
              <Field label={t("builderFieldTypeLabel")}>
                <select
                  value={f.type}
                  onChange={(e) => updateField(f.id, { type: e.target.value as FieldTypeValue })}
                  className={inputClass}
                >
                  <option value="string">{t("builderFieldTypeString")}</option>
                  <option value="number">{t("builderFieldTypeNumber")}</option>
                  <option value="boolean">{t("builderFieldTypeBoolean")}</option>
                  <option value="date">{t("builderFieldTypeDate")}</option>
                  <option value="enum">{t("builderFieldTypeEnum")}</option>
                  <option value="reference">{t("builderFieldTypeReference")}</option>
                  <option value="array">{t("builderFieldTypeArray")}</option>
                </select>
              </Field>
              <label className="flex items-center gap-1 pb-1.5 text-xs text-zinc-600 dark:text-zinc-400">
                <input
                  type="checkbox"
                  checked={f.required}
                  onChange={(e) => updateField(f.id, { required: e.target.checked })}
                />
                {t("builderFieldRequired")}
              </label>
              <button
                type="button"
                data-permission-public="true"
                onClick={() => removeField(f.id)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 text-xs text-zinc-600 dark:border-zinc-700 dark:text-zinc-400"
              >
                {t("removeFieldButton")}
              </button>
            </div>

            {/* Open Points item 38: each new field type's own
                configuration, shown only while that type is selected. */}
            {f.type === "enum" && (
              <div className="flex flex-col gap-1.5 pl-2">
                <span className="text-xs text-zinc-600 dark:text-zinc-400">
                  {t("builderFieldOptionsLabel")}
                </span>
                {f.options.map((option, index) => (
                  <div key={index} className="flex items-center gap-1.5">
                    <input
                      type="text"
                      value={option}
                      onChange={(e) => updateFieldOption(f.id, index, e.target.value)}
                      placeholder={t("builderFieldOptionPlaceholder")}
                      className={inputClass}
                    />
                    <button
                      type="button"
                      data-permission-public="true"
                      onClick={() => removeFieldOption(f.id, index)}
                      className="rounded-md border border-zinc-300 px-2 py-1 text-xs text-zinc-600 dark:border-zinc-700 dark:text-zinc-400"
                    >
                      {t("removeFieldButton")}
                    </button>
                  </div>
                ))}
                <button
                  type="button"
                  data-permission-public="true"
                  onClick={() => addFieldOption(f.id)}
                  className="self-start text-xs font-medium text-zinc-700 underline-offset-4 hover:underline dark:text-zinc-300"
                >
                  {t("builderAddOptionButton")}
                </button>
              </div>
            )}

            {f.type === "reference" && (
              <div className="pl-2">
                <Field label={t("builderFieldReferenceTargetLabel")}>
                  {existingDefinitionKeys.length === 0 ? (
                    <p className="text-xs text-zinc-500 dark:text-zinc-500">
                      {t("builderFieldReferenceNoTargets")}
                    </p>
                  ) : (
                    <select
                      value={f.referenceDefinitionKey}
                      onChange={(e) => updateField(f.id, { referenceDefinitionKey: e.target.value })}
                      className={inputClass}
                    >
                      <option value="">{t("builderFieldReferenceUnselected")}</option>
                      {existingDefinitionKeys.map((key) => (
                        <option key={key} value={key}>
                          {key}
                        </option>
                      ))}
                    </select>
                  )}
                </Field>
              </div>
            )}

            {f.type === "array" && (
              <div className="pl-2">
                <Field label={t("builderFieldArrayItemTypeLabel")}>
                  <select
                    value={f.arrayItemType}
                    onChange={(e) =>
                      updateField(f.id, { arrayItemType: e.target.value as BuilderFieldRow["arrayItemType"] })
                    }
                    className={inputClass}
                  >
                    <option value="string">{t("builderFieldTypeString")}</option>
                    <option value="number">{t("builderFieldTypeNumber")}</option>
                    <option value="boolean">{t("builderFieldTypeBoolean")}</option>
                    <option value="date">{t("builderFieldTypeDate")}</option>
                  </select>
                </Field>
              </div>
            )}
          </div>
        ))}
        <button
          type="button"
          data-permission-public="true"
          onClick={() => setFields((prev) => [...prev, emptyField()])}
          className="self-start text-xs font-medium text-zinc-700 underline-offset-4 hover:underline dark:text-zinc-300"
        >
          {t("builderAddFieldButton")}
        </button>
      </div>

      <div className="flex gap-2">
        <button
          type="submit"
          data-permission-public="true"
          disabled={submitting}
          className="rounded-full bg-foreground px-4 py-1.5 text-xs font-medium text-background transition-colors hover:bg-[#383838] disabled:opacity-50 dark:hover:bg-[#ccc]"
        >
          {t("builderSubmit")}
        </button>
        <button
          type="button"
          data-permission-public="true"
          onClick={() => setOpen(false)}
          className="rounded-full border border-zinc-300 px-4 py-1.5 text-xs font-medium text-zinc-700 dark:border-zinc-700 dark:text-zinc-300"
        >
          {t("builderCancel")}
        </button>
      </div>
    </form>
  );
}

const inputClass =
  "rounded-md border border-zinc-300 bg-white px-3 py-1.5 text-sm text-black dark:border-zinc-700 dark:bg-zinc-900 dark:text-white";

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex min-w-[8rem] flex-col gap-1">
      <label className="text-xs text-zinc-600 dark:text-zinc-400">{label}</label>
      {children}
    </div>
  );
}
