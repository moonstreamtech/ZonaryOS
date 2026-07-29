"use client";

import { useEffect, useState } from "react";
import { useTranslations } from "next-intl";
import type { WizardNode } from "@/lib/wizard";

type Props = {
  locale: string;
};

type ViewState =
  | { status: "loading" }
  | { status: "error" }
  | { status: "question"; node: WizardNode; submitting: boolean }
  | { status: "placeholder"; node: WizardNode }
  | { status: "created"; firmId: string; firmName: string };

// Client-side driver for the wizard's decision tree: fetch a node, render
// it, submit an answer, render whatever node that resolves to. Only ever
// exercises "root" in this slice, but nothing here assumes the tree has
// exactly one level - a deeper tree renders the same way.
export default function WizardClient({ locale }: Props) {
  const t = useTranslations("Wizard");
  const [state, setState] = useState<ViewState>({ status: "loading" });
  const [firmName, setFirmName] = useState("");
  const [firmNameError, setFirmNameError] = useState(false);

  useEffect(() => {
    let cancelled = false;
    fetch("/api/wizard/nodes/root")
      .then((res) => (res.ok ? (res.json() as Promise<WizardNode>) : null))
      .then((node) => {
        if (cancelled) return;
        if (!node) {
          setState({ status: "error" });
          return;
        }
        setState({ status: "question", node, submitting: false });
      })
      .catch(() => {
        if (!cancelled) setState({ status: "error" });
      });
    return () => {
      cancelled = true;
    };
  }, []);

  async function submitAnswer(nodeKey: string, answerValue: string) {
    if (state.status !== "question") return;

    // The wizard's only implemented action (creating the firm) needs a
    // name; the placeholder branch doesn't. Both answers are offered on
    // one screen alongside the firm-name field (see render below), so
    // validation only applies to the branch that actually needs it.
    if (answerValue === "no" && firmName.trim() === "") {
      setFirmNameError(true);
      return;
    }
    setFirmNameError(false);

    setState({ ...state, submitting: true });

    const res = await fetch(`/api/wizard/nodes/${nodeKey}/answer`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ answer: answerValue, firmName }),
    });

    if (!res.ok) {
      setState({ status: "error" });
      return;
    }

    const node = (await res.json()) as WizardNode;
    if (node.kind === "action" && node.result) {
      setState({
        status: "created",
        firmId: node.result.firmId,
        firmName: node.result.firmName,
      });
    } else if (node.kind === "placeholder") {
      setState({ status: "placeholder", node });
    } else {
      setState({ status: "question", node, submitting: false });
    }
  }

  return (
    <main className="flex flex-1 flex-col items-center justify-center gap-6 bg-zinc-50 px-6 py-24 text-center dark:bg-black">
      <h1 className="text-3xl font-semibold tracking-tight text-black dark:text-zinc-50">
        {t("title")}
      </h1>

      {state.status === "loading" && (
        <p className="text-zinc-600 dark:text-zinc-400">…</p>
      )}

      {state.status === "error" && (
        <p className="text-red-600 dark:text-red-400">{t("errorGeneric")}</p>
      )}

      {state.status === "question" && state.node.questionKey && (
        <div className="flex w-full max-w-sm flex-col gap-4">
          <p className="text-lg text-zinc-800 dark:text-zinc-200">
            {t(state.node.questionKey)}
          </p>

          <div className="flex flex-col gap-1 text-left">
            <label
              htmlFor="firmName"
              className="text-sm text-zinc-600 dark:text-zinc-400"
            >
              {t("firmNameLabel")}
            </label>
            <input
              id="firmName"
              type="text"
              value={firmName}
              onChange={(e) => {
                setFirmName(e.target.value);
                setFirmNameError(false);
              }}
              placeholder={t("firmNamePlaceholder")}
              className="rounded-md border border-zinc-300 bg-white px-3 py-2 text-sm text-black dark:border-zinc-700 dark:bg-zinc-900 dark:text-white"
            />
            {firmNameError && (
              <p className="text-sm text-red-600 dark:text-red-400">
                {t("firmNameRequired")}
              </p>
            )}
          </div>

          <div className="flex justify-center gap-3">
            {state.node.answers?.map((answer) => (
              <button
                key={answer.value}
                type="button"
                data-permission-public="true"
                disabled={state.submitting}
                onClick={() => submitAnswer(state.node.key, answer.value)}
                className="rounded-full bg-foreground px-5 py-2 text-sm font-medium text-background transition-colors hover:bg-[#383838] disabled:opacity-50 dark:hover:bg-[#ccc]"
              >
                {t(answer.labelKey)}
              </button>
            ))}
          </div>
        </div>
      )}

      {state.status === "placeholder" && state.node.placeholderKey && (
        <p className="max-w-md text-zinc-700 dark:text-zinc-300">
          {t(state.node.placeholderKey)}
        </p>
      )}

      {state.status === "created" && (
        <div className="flex flex-col items-center gap-4">
          <h2 className="text-xl font-medium text-black dark:text-zinc-50">
            {t("firmCreatedTitle")}
          </h2>
          <p className="text-zinc-700 dark:text-zinc-300">
            {t("firmCreatedBody", { firmName: state.firmName })}
          </p>
          <a
            href={`/${locale}`}
            data-permission-public="true"
            className="rounded-full bg-foreground px-5 py-2 text-sm font-medium text-background transition-colors hover:bg-[#383838] dark:hover:bg-[#ccc]"
          >
            {t("continueToApp")}
          </a>
        </div>
      )}
    </main>
  );
}
