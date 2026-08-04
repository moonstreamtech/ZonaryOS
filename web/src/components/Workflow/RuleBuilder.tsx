"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import type { AvailableAction, FieldSpecInput, RuleInput } from "@/lib/workflow";
import {
  actionRowToRuleAction,
  addChildToLogicNode,
  emptyActionRow,
  emptyLeafNode,
  lookupFieldType,
  nodeToExpressionNode,
  removeNodeById,
  updateNodeById,
  wrapNodeInGroup,
  type BuilderActionRow,
  type BuilderNode,
} from "./ruleBuilderState";

type Props = {
  firmId: string;
  definitionKey: string;
  // fields is the definition's OWN payload schema (Open Points item 35/38)
  // - populates a "field"/"set_field" leaf's field-name dropdown and
  // drives its value input's type (text/number/checkbox), the same
  // schema-aware rendering CreateInstanceForm.tsx already does. Empty for
  // a schema-less definition - the field name/value both fall back to
  // plain text input in that case.
  fields?: FieldSpecInput[];
  // availableActions populates a "transition" action's actionKey
  // dropdown - the same AvailableAction list CurrentState/ListInstances
  // already expose, reused here rather than a second endpoint.
  availableActions: AvailableAction[];
  // existingDefinitionKeys populates a "state" leaf's cross-workflow
  // definitionKey dropdown - the same list DefinitionBuilder.tsx's own
  // "reference" field type target picker already uses (item 38).
  existingDefinitionKeys: string[];
};

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

// The Rule Engine Builder UI (Open Points item 7's frontend batch): lets a
// firm's owner actually create a rule (internal/workflow.CreateRuleForFirm,
// exposed over HTTP this batch) instead of it only being reachable from Go
// fixtures. Reuses DefinitionBuilder.tsx's own collapse/expand/submit
// pattern and input styling rather than inventing new UI primitives - the
// only genuinely new piece is ConditionNodeEditor, the recursive
// AND/OR/NOT + leaf tree editor the design brief calls for.
//
// Owner-gated the same way DefinitionBuilder is: rendered only when the
// caller is an owner (checked by the server component that mounts this),
// tagged data-permission-public rather than data-permission-key - this
// isn't gated by the permission catalog, it's the structural is_owner
// check CreateRuleForFirm itself enforces.
export default function RuleBuilder({ firmId, definitionKey, fields, availableActions, existingDefinitionKeys }: Props) {
  const t = useTranslations("Workflow");
  const router = useRouter();

  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [trigger, setTrigger] = useState<"on_create" | "on_transition">("on_create");
  const [autonomous, setAutonomous] = useState(false);
  const [enabled, setEnabled] = useState(true);
  const [tree, setTree] = useState<BuilderNode>(() => emptyLeafNode());
  const [actions, setActions] = useState<BuilderActionRow[]>([]);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  function resetForm() {
    setName("");
    setTrigger("on_create");
    setAutonomous(false);
    setEnabled(true);
    setTree(emptyLeafNode());
    setActions([]);
    setSubmitError(null);
  }

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setSubmitError(null);

    const rule: RuleInput = {
      name,
      trigger,
      conditionTree: nodeToExpressionNode(tree, fields),
      actions: actions.map((a) => actionRowToRuleAction(a, fields)),
      autonomous,
      enabled,
    };

    setSubmitting(true);
    try {
      const res = await fetch("/api/workflow/rules", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ firmId, definitionKey, rule }),
      });
      if (!res.ok) {
        setSubmitError(
          res.status === 403
            ? t("rulePermissionDenied")
            : t("ruleSubmitError"),
        );
        return;
      }
      setOpen(false);
      resetForm();
      router.refresh();
    } catch {
      setSubmitError(t("ruleSubmitError"));
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
        {t("ruleBuilderOpenButton")}
      </button>
    );
  }

  return (
    <form
      onSubmit={handleSubmit}
      data-permission-public="true"
      className="flex w-full flex-col gap-4 rounded-lg border border-zinc-200 bg-white p-4 text-left dark:border-zinc-800 dark:bg-zinc-950"
    >
      <h2 className="text-sm font-medium text-zinc-700 dark:text-zinc-300">{t("ruleBuilderTitle")}</h2>

      {submitError && <p className="text-sm text-red-600 dark:text-red-400">{submitError}</p>}

      <div className="flex flex-wrap gap-3">
        <Field label={t("ruleNameLabel")}>
          <input
            type="text"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder={t("ruleNamePlaceholder")}
            className={inputClass}
          />
        </Field>
        <Field label={t("ruleTriggerLabel")}>
          <select
            value={trigger}
            onChange={(e) => setTrigger(e.target.value as "on_create" | "on_transition")}
            className={inputClass}
          >
            <option value="on_create">{t("ruleTriggerOnCreate")}</option>
            <option value="on_transition">{t("ruleTriggerOnTransition")}</option>
          </select>
        </Field>
        <label className="flex items-center gap-1 pb-1.5 text-xs text-zinc-600 dark:text-zinc-400">
          <input type="checkbox" checked={autonomous} onChange={(e) => setAutonomous(e.target.checked)} />
          {t("ruleAutonomousLabel")}
        </label>
        <label className="flex items-center gap-1 pb-1.5 text-xs text-zinc-600 dark:text-zinc-400">
          <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} />
          {t("ruleEnabledLabel")}
        </label>
      </div>

      <div className="flex flex-col gap-2">
        <h3 className="text-xs font-medium text-zinc-600 dark:text-zinc-400">{t("ruleConditionTitle")}</h3>
        <ConditionNodeEditor
          node={tree}
          isRoot
          fields={fields}
          existingDefinitionKeys={existingDefinitionKeys}
          onChange={(id, patch) => setTree((prev) => updateNodeById(prev, id, patch))}
          onRemove={(id) => setTree((prev) => removeNodeById(prev, id))}
          onAddChild={(id) => setTree((prev) => addChildToLogicNode(prev, id))}
          onWrap={(id, op) => setTree((prev) => wrapNodeInGroup(prev, id, op))}
        />
      </div>

      <div className="flex flex-col gap-2">
        <h3 className="text-xs font-medium text-zinc-600 dark:text-zinc-400">{t("ruleActionsTitle")}</h3>
        {actions.map((row) => (
          <ActionRowEditor
            key={row.id}
            row={row}
            fields={fields}
            availableActions={availableActions}
            onChange={(patch) =>
              setActions((prev) => prev.map((r) => (r.id === row.id ? { ...r, ...patch } : r)))
            }
            onRemove={() => setActions((prev) => prev.filter((r) => r.id !== row.id))}
          />
        ))}
        <button
          type="button"
          data-permission-public="true"
          onClick={() => setActions((prev) => [...prev, emptyActionRow()])}
          className="self-start text-xs font-medium text-zinc-700 underline-offset-4 hover:underline dark:text-zinc-300"
        >
          {t("ruleAddActionButton")}
        </button>
      </div>

      <div className="flex gap-2">
        <button
          type="submit"
          data-permission-public="true"
          disabled={submitting}
          className="rounded-full bg-foreground px-4 py-1.5 text-xs font-medium text-background transition-colors hover:bg-[#383838] disabled:opacity-50 dark:hover:bg-[#ccc]"
        >
          {t("ruleSubmit")}
        </button>
        <button
          type="button"
          data-permission-public="true"
          onClick={() => {
            setOpen(false);
            resetForm();
          }}
          className="rounded-full border border-zinc-300 px-4 py-1.5 text-xs font-medium text-zinc-700 dark:border-zinc-700 dark:text-zinc-300"
        >
          {t("ruleCancel")}
        </button>
      </div>
    </form>
  );
}

// ConditionNodeEditor is the recursive AND/OR/NOT + leaf tree editor the
// design brief asks for: a logic node renders its operator selector and
// every child (recursively); a leaf renders its type selector and
// type-specific inputs. All state lives in RuleBuilder's own `tree` -
// this component is purely a renderer over it, driven by id-addressed
// callbacks (see ruleBuilderState.ts's mutation helpers).
function ConditionNodeEditor({
  node,
  isRoot,
  fields,
  existingDefinitionKeys,
  onChange,
  onRemove,
  onAddChild,
  onWrap,
}: {
  node: BuilderNode;
  isRoot: boolean;
  fields?: FieldSpecInput[];
  existingDefinitionKeys: string[];
  onChange: (id: number, patch: Partial<BuilderNode>) => void;
  onRemove: (id: number) => void;
  onAddChild: (id: number) => void;
  onWrap: (id: number, op: "and" | "or") => void;
}) {
  const t = useTranslations("Workflow");

  if (node.kind === "logic") {
    return (
      <div className="flex flex-col gap-2 rounded-md border border-zinc-200 p-2 pl-3 dark:border-zinc-800">
        <div className="flex items-center gap-2">
          <select
            value={node.logicOp}
            onChange={(e) => onChange(node.id, { logicOp: e.target.value as BuilderNode["logicOp"] })}
            className={inputClass}
          >
            <option value="and">{t("ruleLogicAnd")}</option>
            <option value="or">{t("ruleLogicOr")}</option>
            <option value="not">{t("ruleLogicNot")}</option>
          </select>
          {node.logicOp !== "not" && (
            <button
              type="button"
              data-permission-public="true"
              onClick={() => onAddChild(node.id)}
              className="text-xs font-medium text-zinc-700 underline-offset-4 hover:underline dark:text-zinc-300"
            >
              {t("ruleAddConditionButton")}
            </button>
          )}
          {!isRoot && (
            <button
              type="button"
              data-permission-public="true"
              onClick={() => onRemove(node.id)}
              className="rounded-md border border-zinc-300 px-2 py-1 text-xs text-zinc-600 dark:border-zinc-700 dark:text-zinc-400"
            >
              {t("removeFieldButton")}
            </button>
          )}
        </div>
        <div className="flex flex-col gap-2 pl-3">
          {node.children.map((child) => (
            <ConditionNodeEditor
              key={child.id}
              node={child}
              isRoot={false}
              fields={fields}
              existingDefinitionKeys={existingDefinitionKeys}
              onChange={onChange}
              onRemove={onRemove}
              onAddChild={onAddChild}
              onWrap={onWrap}
            />
          ))}
        </div>
      </div>
    );
  }

  // Leaf node.
  const selectedFieldType = lookupFieldType(node.field, fields);
  return (
    <div className="flex flex-wrap items-end gap-2 border-t border-zinc-100 pt-2 dark:border-zinc-900">
      <Field label={t("ruleLeafTypeLabel")}>
        <select
          value={node.leafType}
          onChange={(e) => onChange(node.id, { leafType: e.target.value as BuilderNode["leafType"] })}
          className={inputClass}
        >
          <option value="field">{t("ruleLeafField")}</option>
          <option value="state">{t("ruleLeafState")}</option>
          <option value="field_compare">{t("ruleLeafFieldCompare")}</option>
        </select>
      </Field>

      {node.leafType === "field" && (
        <>
          <Field label={t("ruleFieldLabel")}>
            {fields && fields.length > 0 ? (
              <select value={node.field} onChange={(e) => onChange(node.id, { field: e.target.value })} className={inputClass}>
                <option value="">{t("ruleFieldUnselected")}</option>
                {fields.map((f) => (
                  <option key={f.name} value={f.name}>
                    {f.name}
                  </option>
                ))}
              </select>
            ) : (
              <input type="text" value={node.field} onChange={(e) => onChange(node.id, { field: e.target.value })} className={inputClass} />
            )}
          </Field>
          <Field label={t("ruleOperatorLabel")}>
            <select value={node.op} onChange={(e) => onChange(node.id, { op: e.target.value as BuilderNode["op"] })} className={inputClass}>
              <option value="eq">{t("ruleOpEq")}</option>
              <option value="neq">{t("ruleOpNeq")}</option>
              <option value="lt">{t("ruleOpLt")}</option>
              <option value="gt">{t("ruleOpGt")}</option>
              <option value="lte">{t("ruleOpLte")}</option>
              <option value="gte">{t("ruleOpGte")}</option>
              <option value="contains">{t("ruleOpContains")}</option>
            </select>
          </Field>
          <Field label={t("ruleValueLabel")}>
            {selectedFieldType === "boolean" ? (
              <select value={node.value} onChange={(e) => onChange(node.id, { value: e.target.value })} className={inputClass}>
                <option value="true">{t("ruleValueTrue")}</option>
                <option value="false">{t("ruleValueFalse")}</option>
              </select>
            ) : selectedFieldType === "enum" ? (
              <select value={node.value} onChange={(e) => onChange(node.id, { value: e.target.value })} className={inputClass}>
                <option value="">{t("ruleFieldUnselected")}</option>
                {(fields?.find((f) => f.name === node.field)?.options ?? []).map((opt) => (
                  <option key={opt} value={opt}>
                    {opt}
                  </option>
                ))}
              </select>
            ) : (
              <input
                type={selectedFieldType === "number" ? "number" : "text"}
                value={node.value}
                onChange={(e) => onChange(node.id, { value: e.target.value })}
                className={inputClass}
              />
            )}
          </Field>
        </>
      )}

      {node.leafType === "state" && (
        <>
          <Field label={t("ruleStateDefinitionKeyLabel")}>
            {existingDefinitionKeys.length > 0 ? (
              <select
                value={node.definitionKey}
                onChange={(e) => onChange(node.id, { definitionKey: e.target.value })}
                className={inputClass}
              >
                <option value="">{t("ruleFieldUnselected")}</option>
                {existingDefinitionKeys.map((key) => (
                  <option key={key} value={key}>
                    {key}
                  </option>
                ))}
              </select>
            ) : (
              <input
                type="text"
                value={node.definitionKey}
                onChange={(e) => onChange(node.id, { definitionKey: e.target.value })}
                className={inputClass}
              />
            )}
          </Field>
          <Field label={t("ruleStateInstanceIdFieldLabel")}>
            <input
              type="text"
              value={node.instanceIdField}
              onChange={(e) => onChange(node.id, { instanceIdField: e.target.value })}
              className={inputClass}
            />
          </Field>
          <Field label={t("ruleStateLabel")}>
            <input type="text" value={node.state} onChange={(e) => onChange(node.id, { state: e.target.value })} className={inputClass} />
          </Field>
        </>
      )}

      {node.leafType === "field_compare" && (
        <>
          <Field label={t("ruleFieldALabel")}>
            {fields && fields.length > 0 ? (
              <select value={node.fieldA} onChange={(e) => onChange(node.id, { fieldA: e.target.value })} className={inputClass}>
                <option value="">{t("ruleFieldUnselected")}</option>
                {fields.map((f) => (
                  <option key={f.name} value={f.name}>
                    {f.name}
                  </option>
                ))}
              </select>
            ) : (
              <input type="text" value={node.fieldA} onChange={(e) => onChange(node.id, { fieldA: e.target.value })} className={inputClass} />
            )}
          </Field>
          <Field label={t("ruleOperatorLabel")}>
            <select value={node.op} onChange={(e) => onChange(node.id, { op: e.target.value as BuilderNode["op"] })} className={inputClass}>
              <option value="eq">{t("ruleOpEq")}</option>
              <option value="neq">{t("ruleOpNeq")}</option>
              <option value="lt">{t("ruleOpLt")}</option>
              <option value="gt">{t("ruleOpGt")}</option>
              <option value="lte">{t("ruleOpLte")}</option>
              <option value="gte">{t("ruleOpGte")}</option>
            </select>
          </Field>
          <Field label={t("ruleFieldBLabel")}>
            {fields && fields.length > 0 ? (
              <select value={node.fieldB} onChange={(e) => onChange(node.id, { fieldB: e.target.value })} className={inputClass}>
                <option value="">{t("ruleFieldUnselected")}</option>
                {fields.map((f) => (
                  <option key={f.name} value={f.name}>
                    {f.name}
                  </option>
                ))}
              </select>
            ) : (
              <input type="text" value={node.fieldB} onChange={(e) => onChange(node.id, { fieldB: e.target.value })} className={inputClass} />
            )}
          </Field>
        </>
      )}

      <div className="flex gap-1">
        <button
          type="button"
          data-permission-public="true"
          onClick={() => onWrap(node.id, "and")}
          className="rounded-md border border-zinc-300 px-2 py-1.5 text-xs text-zinc-600 dark:border-zinc-700 dark:text-zinc-400"
        >
          {t("ruleGroupButton")}
        </button>
        {!isRoot && (
          <button
            type="button"
            data-permission-public="true"
            onClick={() => onRemove(node.id)}
            className="rounded-md border border-zinc-300 px-2 py-1.5 text-xs text-zinc-600 dark:border-zinc-700 dark:text-zinc-400"
          >
            {t("removeFieldButton")}
          </button>
        )}
      </div>
    </div>
  );
}

function ActionRowEditor({
  row,
  fields,
  availableActions,
  onChange,
  onRemove,
}: {
  row: BuilderActionRow;
  fields?: FieldSpecInput[];
  availableActions: AvailableAction[];
  onChange: (patch: Partial<BuilderActionRow>) => void;
  onRemove: () => void;
}) {
  const t = useTranslations("Workflow");
  const selectedFieldType = lookupFieldType(row.field, fields);

  return (
    <div className="flex flex-wrap items-end gap-2 border-t border-zinc-100 pt-2 dark:border-zinc-900">
      <Field label={t("ruleActionTypeLabel")}>
        <select value={row.type} onChange={(e) => onChange({ type: e.target.value as BuilderActionRow["type"] })} className={inputClass}>
          <option value="transition">{t("ruleActionTransition")}</option>
          <option value="notify">{t("ruleActionNotify")}</option>
          <option value="set_field">{t("ruleActionSetField")}</option>
        </select>
      </Field>

      {row.type === "transition" && (
        <Field label={t("ruleActionKeyLabel")}>
          {availableActions.length > 0 ? (
            <select value={row.actionKey} onChange={(e) => onChange({ actionKey: e.target.value })} className={inputClass}>
              <option value="">{t("ruleFieldUnselected")}</option>
              {availableActions.map((a) => (
                <option key={a.actionKey} value={a.actionKey}>
                  {a.name} ({a.actionKey})
                </option>
              ))}
            </select>
          ) : (
            <input type="text" value={row.actionKey} onChange={(e) => onChange({ actionKey: e.target.value })} className={inputClass} />
          )}
        </Field>
      )}

      {row.type === "notify" && (
        <Field label={t("ruleMessageTemplateLabel")}>
          <input
            type="text"
            value={row.messageTemplate}
            onChange={(e) => onChange({ messageTemplate: e.target.value })}
            placeholder={t("ruleMessageTemplatePlaceholder")}
            className={inputClass}
          />
        </Field>
      )}

      {row.type === "set_field" && (
        <>
          <Field label={t("ruleActionFieldLabel")}>
            {fields && fields.length > 0 ? (
              <select value={row.field} onChange={(e) => onChange({ field: e.target.value })} className={inputClass}>
                <option value="">{t("ruleFieldUnselected")}</option>
                {fields.map((f) => (
                  <option key={f.name} value={f.name}>
                    {f.name}
                  </option>
                ))}
              </select>
            ) : (
              <input type="text" value={row.field} onChange={(e) => onChange({ field: e.target.value })} className={inputClass} />
            )}
          </Field>
          <Field label={t("ruleActionValueLabel")}>
            {selectedFieldType === "boolean" ? (
              <select value={row.value} onChange={(e) => onChange({ value: e.target.value })} className={inputClass}>
                <option value="true">{t("ruleValueTrue")}</option>
                <option value="false">{t("ruleValueFalse")}</option>
              </select>
            ) : (
              <input
                type={selectedFieldType === "number" ? "number" : "text"}
                value={row.value}
                onChange={(e) => onChange({ value: e.target.value })}
                className={inputClass}
              />
            )}
          </Field>
        </>
      )}

      <button
        type="button"
        data-permission-public="true"
        onClick={onRemove}
        className="rounded-md border border-zinc-300 px-2 py-1.5 text-xs text-zinc-600 dark:border-zinc-700 dark:text-zinc-400"
      >
        {t("removeFieldButton")}
      </button>
    </div>
  );
}
