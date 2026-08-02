declare module "virtual:help-docs" {
  const data: import("./help-types").HelpData;
  export default data;
}

declare module "virtual:help-search-index" {
  const index: import("./help-types").HelpSearchEntry[];
  export default index;
}
