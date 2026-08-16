"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import type { Warehouse, Location, WarehouseStockEntry, LocationType } from "@/lib/warehouse";

type Props = {
  firmId: string;
  warehouse: Warehouse;
  locations: Location[];
  stock: WarehouseStockEntry[];
  isOwner: boolean;
};

const LOCATION_TYPES: LocationType[] = ["aisle", "shelf", "bin", "staging", "dispatch"];

type LocationTreeProps = {
  locations: Location[];
  parentId: string | undefined;
  t: ReturnType<typeof useTranslations>;
  depth: number;
};

// Aisle -> shelf -> bin tree, built client-side from each location's own
// `parentId` (see lib/warehouse.ts's fetchWarehouseLocations doc comment -
// the backend only ever returns a flat list). Each level is independently
// collapsible; a location with no children renders without a toggle.
function LocationTree({ locations, parentId, t, depth }: LocationTreeProps) {
  const children = locations.filter((l) => l.parentId === parentId);
  if (children.length === 0) return null;

  return (
    <ul className={depth === 0 ? "flex flex-col gap-1" : "mt-1 ml-5 flex flex-col gap-1 border-l border-zinc-200 pl-3 dark:border-zinc-800"}>
      {children.map((loc) => (
        <LocationNode key={loc.id} location={loc} locations={locations} t={t} depth={depth} />
      ))}
    </ul>
  );
}

function LocationNode({
  location,
  locations,
  t,
  depth,
}: {
  location: Location;
  locations: Location[];
  t: ReturnType<typeof useTranslations>;
  depth: number;
}) {
  const [expanded, setExpanded] = useState(true);
  const hasChildren = locations.some((l) => l.parentId === location.id);

  return (
    <li>
      <div className="flex items-center gap-2 py-0.5 text-sm">
        {hasChildren ? (
          <button
            type="button"
            data-permission-public="true"
            onClick={() => setExpanded((v) => !v)}
            aria-label={expanded ? t("collapse") : t("expand")}
            className="w-4 text-zinc-500 dark:text-zinc-400"
          >
            {expanded ? "−" : "+"}
          </button>
        ) : (
          <span className="w-4" aria-hidden="true" />
        )}
        <span className="font-mono text-xs text-zinc-500 dark:text-zinc-400">{location.code}</span>
        <span className="text-black dark:text-zinc-50">{location.name ?? location.code}</span>
        <span className="text-xs text-zinc-500 dark:text-zinc-400">
          {t(`locationTypes.${location.type}`)}
        </span>
        {!location.isActive && (
          <span className="text-xs text-zinc-400 dark:text-zinc-600">{t("statusInactive")}</span>
        )}
      </div>
      {expanded && <LocationTree locations={locations} parentId={location.id} t={t} depth={depth + 1} />}
    </li>
  );
}

// The /inventory/warehouses/[warehouseId] detail page: the location tree,
// a create-location form (owner-only), and stock per location. Mirrors
// components/Manufacturing/BOMDetail.tsx's own layout/action conventions.
export default function WarehouseDetail({ firmId, warehouse, locations, stock, isOwner }: Props) {
  const t = useTranslations("Warehouse");
  const router = useRouter();

  const [creating, setCreating] = useState(false);
  const [code, setCode] = useState("");
  const [name, setName] = useState("");
  const [type, setType] = useState<LocationType>("bin");
  const [parentId, setParentId] = useState("");
  const [createError, setCreateError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  async function submitCreate(e: FormEvent) {
    e.preventDefault();
    setCreateError(null);
    if (!code.trim()) {
      setCreateError(t("createRequired"));
      return;
    }

    setSubmitting(true);
    try {
      const res = await fetch(`/api/warehouse/warehouses/${firmId}/${warehouse.id}/locations`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          code: code.trim(),
          name: name.trim() || undefined,
          type,
          parentId: parentId || undefined,
        }),
      });
      if (!res.ok) {
        setCreateError(t("createError"));
        return;
      }
      setCreating(false);
      setCode("");
      setName("");
      setType("bin");
      setParentId("");
      router.refresh();
    } catch {
      setCreateError(t("createError"));
    } finally {
      setSubmitting(false);
    }
  }

  function productLabel(entry: WarehouseStockEntry): string {
    return entry.productSku;
  }

  return (
    <div className="flex w-full max-w-2xl flex-col gap-6">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold tracking-tight text-black dark:text-zinc-50">{warehouse.name}</h1>
        <span className="text-sm text-zinc-600 dark:text-zinc-400">
          {warehouse.isActive ? t("statusActive") : t("statusInactive")}
        </span>
      </div>

      {warehouse.address && (
        <div className="panel flex flex-col gap-2 p-4 text-sm">
          <div className="flex justify-between">
            <span className="text-zinc-600 dark:text-zinc-400">{t("address")}</span>
            <span>{warehouse.address}</span>
          </div>
        </div>
      )}

      <div className="panel flex flex-col gap-3 p-4">
        <div className="flex items-center justify-between">
          <h2 className="text-sm font-medium text-zinc-500 dark:text-zinc-400">{t("locationsTitle")}</h2>
          {isOwner && (
            <button
              type="button"
              data-permission-public="true"
              onClick={() => setCreating((v) => !v)}
              className="rounded-md border border-zinc-300 px-3 py-1.5 text-sm font-medium text-black hover:bg-zinc-100 dark:border-zinc-700 dark:text-zinc-50 dark:hover:bg-zinc-900"
            >
              {creating ? t("cancel") : t("newLocation")}
            </button>
          )}
        </div>

        {creating && (
          <form
            onSubmit={submitCreate}
            data-permission-public="true"
            className="flex flex-col gap-3 rounded-md border border-zinc-300 p-4 dark:border-zinc-700"
          >
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
              <label className="flex flex-col gap-1 text-sm">
                {t("locationCode")}
                <input
                  value={code}
                  onChange={(e) => setCode(e.target.value)}
                  className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                />
              </label>
              <label className="flex flex-col gap-1 text-sm">
                {t("locationName")}
                <input
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                />
              </label>
              <label className="flex flex-col gap-1 text-sm">
                {t("locationType")}
                <select
                  value={type}
                  onChange={(e) => setType(e.target.value as LocationType)}
                  className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                >
                  {LOCATION_TYPES.map((lt) => (
                    <option key={lt} value={lt}>
                      {t(`locationTypes.${lt}`)}
                    </option>
                  ))}
                </select>
              </label>
              <label className="flex flex-col gap-1 text-sm">
                {t("parentLocation")}
                <select
                  value={parentId}
                  onChange={(e) => setParentId(e.target.value)}
                  className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                >
                  <option value="">{t("noParent")}</option>
                  {locations.map((l) => (
                    <option key={l.id} value={l.id}>
                      {l.code} {l.name ? `– ${l.name}` : ""}
                    </option>
                  ))}
                </select>
              </label>
            </div>
            {createError && <p className="text-sm text-red-600 dark:text-red-400">{createError}</p>}
            <button
              type="submit"
              data-permission-public="true"
              disabled={submitting}
              className="self-start rounded-md bg-black px-3 py-1.5 text-sm font-medium text-white disabled:opacity-50 dark:bg-zinc-50 dark:text-black"
            >
              {t("create")}
            </button>
          </form>
        )}

        {locations.length === 0 ? (
          <p className="text-sm text-zinc-600 dark:text-zinc-400">{t("locationsEmpty")}</p>
        ) : (
          <LocationTree locations={locations} parentId={undefined} t={t} depth={0} />
        )}
      </div>

      <div className="panel flex flex-col gap-2 p-0">
        <h2 className="px-4 pt-4 text-sm font-medium text-zinc-500 dark:text-zinc-400">{t("stockTitle")}</h2>
        {stock.length === 0 ? (
          <p className="px-4 pb-4 text-sm text-zinc-600 dark:text-zinc-400">{t("noStock")}</p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full border-collapse text-left text-sm">
              <thead>
                <tr className="border-b border-zinc-300 text-zinc-600 dark:border-zinc-700 dark:text-zinc-400">
                  <th className="py-2 pr-4 pl-4 font-medium">{t("columnLocation")}</th>
                  <th className="py-2 pr-4 font-medium">{t("columnProduct")}</th>
                  <th className="py-2 pr-4 font-medium">{t("columnQuantity")}</th>
                </tr>
              </thead>
              <tbody>
                {stock.map((s) => (
                  <tr
                    key={`${s.locationId}-${s.productId}`}
                    className="border-b border-zinc-200 last:border-0 dark:border-zinc-800"
                  >
                    <td className="py-2 pr-4 pl-4 font-mono text-xs">{s.locationCode}</td>
                    <td className="py-2 pr-4 font-mono text-xs">{productLabel(s)}</td>
                    <td className="py-2 pr-4 font-mono">{s.quantity}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  );
}
