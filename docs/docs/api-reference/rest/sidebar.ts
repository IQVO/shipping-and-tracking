import type { SidebarsConfig } from "@docusaurus/plugin-content-docs";

const sidebar: SidebarsConfig = {
  apisidebar: [
    {
      type: "doc",
      id: "api-reference/rest/shipping-tracking-api",
    },
    {
      type: "category",
      label: "Labels",
      link: {
        type: "doc",
        id: "api-reference/rest/labels",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/issue-label",
          label: "Issue a shipping label for one package (IssueLabel)",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/void-label",
          label: "Void an issued label (VoidLabel)",
          className: "api-method post",
        },
      ],
    },
    {
      type: "category",
      label: "Journeys",
      link: {
        type: "doc",
        id: "api-reference/rest/journeys",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/get-journey",
          label: "Get the journey of one package (GetJourney)",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/record-scan",
          label: "Record one scan on a package journey (RecordScan)",
          className: "api-method post",
        },
      ],
    },
  ],
};

export default sidebar.apisidebar;
