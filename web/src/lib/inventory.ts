// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Inventory management, warehouse stock, and supplier management,
// frontend side: products, stock levels/movements, and suppliers
// (internal/inventory.Product/StockLevel/StockMovement/Supplier) - mirrors
// lib/hr.ts's own "types + fetch helpers, one file per backend package"
// convention.

export type Product = {
  id: string;
  sku: string;
  name: string;
  description?: string;
  unit: string;
  unitPrice?: string;
  costPrice?: string;
  taxRate?: string;
  category?: string;
  minQuantity: string;
  customFields: Record<string, unknown>;
  isActive: boolean;
  createdAt: string;
};

export type StockLevel = {
  productId: string;
  location: string;
  quantity: string;
  reservedQuantity: string;
  updatedAt: string;
};

export type StockMovement = {
  id: string;
  productId: string;
  location: string;
  quantityChange: string;
  reason: string;
  sourceType?: string;
  sourceId?: string;
  createdAt: string;
};

export type Supplier = {
  id: string;
  name: string;
  contactName?: string;
  email?: string;
  phone?: string;
  address?: string;
  currency: string;
  paymentTerms?: string;
  customFields: Record<string, unknown>;
  isActive: boolean;
  createdAt: string;
};

function apiBase(): string {
  return process.env.ZONARYOS_API_BASE_URL ?? "http://localhost:8080";
}

/** Calls the Go backend's `GET /api/firms/{firmId}/products`. */
export async function fetchProducts(
  token: string,
  firmId: string,
  opts: { activeOnly?: boolean } = {},
): Promise<Product[] | null> {
  try {
    const params = new URLSearchParams();
    if (opts.activeOnly) params.set("activeOnly", "true");
    const qs = params.toString();
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/products${qs ? `?${qs}` : ""}`,
      { headers: { Authorization: `Bearer ${token}` }, cache: "no-store" },
    );
    if (!res.ok) return null;
    return (await res.json()) as Product[];
  } catch {
    return null;
  }
}

export type CreateProductInput = {
  sku: string;
  name: string;
  description?: string;
  unit?: string;
  unitPrice?: string;
  costPrice?: string;
  taxRate?: string;
  category?: string;
  minQuantity?: string;
};

/** Calls the Go backend's `POST /api/firms/{firmId}/products` (owner-only). */
export async function createProduct(
  token: string,
  firmId: string,
  input: CreateProductInput,
): Promise<{ ok: true; product: Product } | { ok: false; error: string; status: number }> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/products`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
      body: JSON.stringify(input),
      cache: "no-store",
    });
    if (!res.ok) {
      const text = await res.text();
      return { ok: false, error: text || `Request failed (${res.status})`, status: res.status };
    }
    return { ok: true, product: (await res.json()) as Product };
  } catch {
    return { ok: false, error: "network error", status: 0 };
  }
}

export type UpdateProductInput = Partial<{
  name: string;
  description: string;
  unit: string;
  unitPrice: string;
  costPrice: string;
  taxRate: string;
  category: string;
  minQuantity: string;
  isActive: boolean;
}>;

/** Calls the Go backend's `PATCH /api/firms/{firmId}/products/{productId}` (owner-only). */
export async function updateProduct(
  token: string,
  firmId: string,
  productId: string,
  patch: UpdateProductInput,
): Promise<{ ok: true; product: Product } | { ok: false; error: string; status: number }> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/products/${encodeURIComponent(productId)}`,
      {
        method: "PATCH",
        headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
        body: JSON.stringify(patch),
        cache: "no-store",
      },
    );
    if (!res.ok) {
      const text = await res.text();
      return { ok: false, error: text || `Request failed (${res.status})`, status: res.status };
    }
    return { ok: true, product: (await res.json()) as Product };
  } catch {
    return { ok: false, error: "network error", status: 0 };
  }
}

/** Calls the Go backend's `GET /api/firms/{firmId}/products/{productId}/stock`. */
export async function fetchStock(token: string, firmId: string, productId: string): Promise<StockLevel[] | null> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/products/${encodeURIComponent(productId)}/stock`,
      { headers: { Authorization: `Bearer ${token}` }, cache: "no-store" },
    );
    if (!res.ok) return null;
    return (await res.json()) as StockLevel[];
  } catch {
    return null;
  }
}

export type ListStockMovementsResult = { movements: StockMovement[]; total: number };

/** Calls the Go backend's `GET /api/firms/{firmId}/stock-movements` (paginated). */
export async function fetchStockMovements(
  token: string,
  firmId: string,
  opts: { limit?: number; offset?: number; productId?: string } = {},
): Promise<ListStockMovementsResult | null> {
  try {
    const params = new URLSearchParams();
    if (opts.limit) params.set("limit", String(opts.limit));
    if (opts.offset) params.set("offset", String(opts.offset));
    if (opts.productId) params.set("productId", opts.productId);
    const qs = params.toString();
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/stock-movements${qs ? `?${qs}` : ""}`,
      { headers: { Authorization: `Bearer ${token}` }, cache: "no-store" },
    );
    if (!res.ok) return null;
    return (await res.json()) as ListStockMovementsResult;
  } catch {
    return null;
  }
}

/** Calls the Go backend's `GET /api/firms/{firmId}/suppliers`. */
export async function fetchSuppliers(
  token: string,
  firmId: string,
  opts: { activeOnly?: boolean } = {},
): Promise<Supplier[] | null> {
  try {
    const params = new URLSearchParams();
    if (opts.activeOnly) params.set("activeOnly", "true");
    const qs = params.toString();
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/suppliers${qs ? `?${qs}` : ""}`,
      { headers: { Authorization: `Bearer ${token}` }, cache: "no-store" },
    );
    if (!res.ok) return null;
    return (await res.json()) as Supplier[];
  } catch {
    return null;
  }
}

export type CreateSupplierInput = {
  name: string;
  contactName?: string;
  email?: string;
  phone?: string;
  address?: string;
  currency?: string;
  paymentTerms?: string;
};

/** Calls the Go backend's `POST /api/firms/{firmId}/suppliers` (owner-only). */
export async function createSupplier(
  token: string,
  firmId: string,
  input: CreateSupplierInput,
): Promise<{ ok: true; supplier: Supplier } | { ok: false; error: string; status: number }> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/suppliers`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
      body: JSON.stringify(input),
      cache: "no-store",
    });
    if (!res.ok) {
      const text = await res.text();
      return { ok: false, error: text || `Request failed (${res.status})`, status: res.status };
    }
    return { ok: true, supplier: (await res.json()) as Supplier };
  } catch {
    return { ok: false, error: "network error", status: 0 };
  }
}

export type UpdateSupplierInput = Partial<{
  name: string;
  contactName: string;
  email: string;
  phone: string;
  address: string;
  currency: string;
  paymentTerms: string;
  isActive: boolean;
}>;

/** Calls the Go backend's `PATCH /api/firms/{firmId}/suppliers/{supplierId}` (owner-only). */
export async function updateSupplier(
  token: string,
  firmId: string,
  supplierId: string,
  patch: UpdateSupplierInput,
): Promise<{ ok: true; supplier: Supplier } | { ok: false; error: string; status: number }> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/suppliers/${encodeURIComponent(supplierId)}`,
      {
        method: "PATCH",
        headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
        body: JSON.stringify(patch),
        cache: "no-store",
      },
    );
    if (!res.ok) {
      const text = await res.text();
      return { ok: false, error: text || `Request failed (${res.status})`, status: res.status };
    }
    return { ok: true, supplier: (await res.json()) as Supplier };
  } catch {
    return { ok: false, error: "network error", status: 0 };
  }
}
