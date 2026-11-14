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
          id: "api-reference/rest/issue-return-label",
          label: "Issue the reverse label of one package of a return (IssueReturnLabel)",
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
        {
          type: "doc",
          id: "api-reference/rest/report-carrier-event",
          label: "Report one external carrier event (ReportCarrierEvent, ADR 0015)",
          className: "api-method post",
        },
      ],
    },
    {
      type: "category",
      label: "reports",
      link: {
        type: "doc",
        id: "api-reference/rest/reports",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/get-package-touches-report",
          label: "Touches per delivered package, per day and tier",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-delivery-outcomes-report",
          label: "Delivery outcomes and first-attempt success, per day",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-report-freshness",
          label: "How far the analytics projection is behind",
          className: "api-method get",
        },
      ],
    },
  ],
};

export default sidebar.apisidebar;
