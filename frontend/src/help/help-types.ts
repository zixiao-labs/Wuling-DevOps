export interface HelpDoc {
  slug: string;
  title: string;
  group: string;
  order: number;
  description: string;
  html: string;
  headings: { level: number; id: string; text: string }[];
  updatedAt: string;
}

export interface HelpNavGroup {
  group: string;
  items: { slug: string; title: string }[];
}

export interface HelpData {
  docs: HelpDoc[];
  nav: HelpNavGroup[];
  buildId: string;
}

export interface HelpSearchEntry {
  slug: string;
  title: string;
  group: string;
  description: string;
  text: string;
}
