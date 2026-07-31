// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { cookies } from "next/headers";
import { getTranslations, setRequestLocale } from "next-intl/server";
import { fetchMe } from "@/lib/me";
import { lookupInvite } from "@/lib/invite";
import AcceptInviteButton from "@/components/Invite/AcceptInviteButton";

type PageProps = {
  params: Promise<{ locale: string; token: string }>;
};

// Kept as a module-level component (not declared inside InvitePage) so
// its identity is stable across renders - every branch below shares this
// same page chrome (title + centered layout), only the body differs.
function InviteShell({
  title,
  children,
}: {
  title: string;
  children: React.ReactNode;
}) {
  return (
    <main className="flex flex-1 flex-col items-center justify-center gap-4 bg-zinc-50 px-6 py-24 text-center dark:bg-black">
      <h1 className="text-2xl font-semibold text-black dark:text-zinc-50">{title}</h1>
      {children}
    </main>
  );
}

// Open Points item 17's companion gap, resolved without any email
// infrastructure (docs/DEVELOPMENT.md's Invites section): a public,
// unauthenticated page - deliberately NOT rendered inside the firm-scoped
// nav shell's assumptions (it never calls requireFirmContext, and works
// for a visitor with zero firm memberships, unlike every page under
// /settings). lookupInvite (GET /api/invites/{token}, no bearer token at
// all) is called directly here, the same "server component calls a
// lib/ fetch helper directly, no Next.js proxy route needed" shape
// /settings/members already uses for its own read-only data - a proxy
// route only exists for this feature's *mutations* (create/revoke/accept),
// which need to inject a bearer token a client component can't read
// itself (see /api/invites/[firmId]/route.ts and
// /api/invite-accept/[token]/route.ts).
export default async function InvitePage({ params }: PageProps) {
  const { locale, token } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("Invite");

  const result = await lookupInvite(token);

  const returnTo = `/${locale}/invite/${token}`;
  const loginHref = `/api/auth/login?returnTo=${encodeURIComponent(returnTo)}`;
  const registerHref = `/api/auth/login?mode=register&returnTo=${encodeURIComponent(returnTo)}`;

  const title = t("title");

  if (result === null) {
    return (
      <InviteShell title={title}>
        <p className="text-red-600 dark:text-red-400">{t("networkError")}</p>
      </InviteShell>
    );
  }

  if (!result.ok) {
    // Only ErrInviteNotFound (a token that never existed / was mistyped)
    // reaches this branch - internal/invite.Lookup returns every other
    // state (used/revoked/pending-but-expired) as a successful response
    // with Info.Status/Expired describing why, handled below instead of
    // here. See internal/invite/handlers.go's writeError.
    return (
      <InviteShell title={title}>
        <p className="text-zinc-600 dark:text-zinc-400">{t("notFound")}</p>
      </InviteShell>
    );
  }

  const { info } = result;

  if (info.status === "used") {
    return (
      <InviteShell title={title}>
        <p className="text-zinc-600 dark:text-zinc-400">{t("alreadyUsed")}</p>
      </InviteShell>
    );
  }
  if (info.status === "revoked") {
    return (
      <InviteShell title={title}>
        <p className="text-zinc-600 dark:text-zinc-400">{t("revoked")}</p>
      </InviteShell>
    );
  }
  if (info.expired) {
    return (
      <InviteShell title={title}>
        <p className="text-zinc-600 dark:text-zinc-400">{t("expired")}</p>
      </InviteShell>
    );
  }

  const sessionToken = (await cookies()).get("zonaryos_session")?.value;
  const me = sessionToken ? await fetchMe(sessionToken) : null;

  return (
    <InviteShell title={title}>
      <p className="max-w-md text-lg text-zinc-600 dark:text-zinc-400">
        {t("invitedTo", { firmName: info.firmName, roleName: info.roleName })}
      </p>
      {me ? (
        <AcceptInviteButton locale={locale} token={token} />
      ) : (
        <div className="flex flex-col items-center gap-3">
          <p className="text-sm text-zinc-500 dark:text-zinc-500">{t("signInPrompt")}</p>
          <div className="flex flex-wrap items-center justify-center gap-3">
            <a
              href={loginHref}
              data-permission-public="true"
              className="rounded-full bg-foreground px-5 py-2 text-sm font-medium text-background transition-colors hover:bg-[#383838] dark:hover:bg-[#ccc]"
            >
              {t("signInButton")}
            </a>
            <a
              href={registerHref}
              data-permission-public="true"
              className="rounded-full border border-zinc-300 px-5 py-2 text-sm font-medium text-black dark:border-zinc-700 dark:text-zinc-50"
            >
              {t("createAccountButton")}
            </a>
          </div>
        </div>
      )}
    </InviteShell>
  );
}
