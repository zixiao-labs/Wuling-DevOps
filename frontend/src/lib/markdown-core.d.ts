export declare function escapeHtml(s: string): string;
export declare function slugify(text: string): string;

export interface RenderDocOptions {
  links?: boolean;
  headingIds?: boolean;
  hr?: boolean;
  tables?: boolean;
}

export interface DocHeading {
  level: number;
  id: string;
  text: string;
}

export declare function renderDoc(
  raw: string,
  opts?: RenderDocOptions,
): { html: string; headings: DocHeading[] };

export declare function renderBlocks(raw: string): string;

export declare function parseFrontMatter(raw: string): {
  meta: Record<string, string | number>;
  body: string;
};

export declare function toPlainText(raw: string): string;
