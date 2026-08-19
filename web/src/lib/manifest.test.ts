// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// PWA foundation batch, Part 2: manifest.json is valid JSON and carries
// the fields a PWA manifest needs to be installable
// (name/icons/start_url/display/theme_color) - this repo's Tests section
// asks for exactly this check.
import fs from "fs";
import path from "path";
import { fileURLToPath } from "url";
import { describe, expect, it } from "vitest";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const MANIFEST_PATH = path.resolve(__dirname, "..", "..", "public", "manifest.json");

describe("manifest.json", () => {
  const raw = fs.readFileSync(MANIFEST_PATH, "utf8");

  it("is valid JSON", () => {
    expect(() => JSON.parse(raw)).not.toThrow();
  });

  it("has the required PWA manifest fields", () => {
    const manifest = JSON.parse(raw) as Record<string, unknown>;

    expect(typeof manifest.name).toBe("string");
    expect(manifest.name).toBe("ZonaryOS");
    expect(manifest.start_url).toBe("/");
    expect(manifest.display).toBe("standalone");
    expect(manifest.theme_color).toBe("#18181b");
    expect(manifest.background_color).toBe("#18181b");

    expect(Array.isArray(manifest.icons)).toBe(true);
    const icons = manifest.icons as Array<Record<string, unknown>>;
    expect(icons.length).toBeGreaterThan(0);
    for (const icon of icons) {
      expect(typeof icon.src).toBe("string");
      expect(typeof icon.sizes).toBe("string");
      expect(typeof icon.type).toBe("string");
    }
  });
});
