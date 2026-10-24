import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { Card } from "@warehouse/ui-kit";
import { Form, FormRow, SubmitButton, TextField } from "../components/formkit";
import { PageHeader, Stack } from "../components/layout";
import { trackingNumberProblem } from "../lib";

/** `journeys`: a lookup by tracking number — the one identifier a shopper or
 *  an operator holds. No list endpoint exists in the contract, so the screen
 *  is a lookup, not a table. */
export function JourneyLookupScreen() {
  const [tn, setTn] = useState("");
  const navigate = useNavigate();
  const id = tn.trim();
  const problem = id === "" ? null : trackingNumberProblem(id);
  const go = () => {
    if (id !== "" && problem === null) navigate(encodeURIComponent(id));
  };
  return (
    <Stack gap={5}>
      <PageHeader
        title="Package journeys"
        subtitle="Where one package stands, and every scan that moved it — read-only"
      />
      <Card title="Look up a journey">
        <Form label="Journey lookup" onSubmit={go}>
          <FormRow>
            <TextField
              label="Tracking number"
              value={tn}
              onChange={setTn}
              placeholder="RN0123456789ABCDEF"
              hint={problem ?? "The tracking number the label was issued with"}
            />
            <SubmitButton disabled={id === "" || problem !== null}>View journey</SubmitButton>
          </FormRow>
        </Form>
      </Card>
    </Stack>
  );
}
