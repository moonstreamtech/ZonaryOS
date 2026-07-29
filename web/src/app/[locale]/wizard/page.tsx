// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { cookies } from "next/headers";
import { redirect } from "next/navigation";
import { setRequestLocale } from "next-intl/server";
import { fetchMe } from "@/lib/me";
import WizardClient from "./WizardClient";

type PageProps = {
  params: Promise<{ locale: string }>;
};

// Server-side gate mirroring the routing rule in the homepage: only a
// signed-in user with zero firm memberships belongs here (Vision §3's
// wizard trigger). Anyone else - unauthenticated, or already a member of
// a firm - is redirected back to the (currently placeholder) main app.
export default async function WizardPage({ params }: PageProps) {
  const { locale } = await params;
  setRequestLocale(locale);

  const sessionToken = (await cookies()).get("zonaryos_session")?.value;
  const me = sessionToken ? await fetchMe(sessionToken) : null;

  if (!me) {
    redirect(`/${locale}`);
  }
  if (me.firms.length > 0) {
    redirect(`/${locale}`);
  }

  return <WizardClient locale={locale} />;
}
