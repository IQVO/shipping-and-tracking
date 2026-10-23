Feature: A tracking number collision that survives the retry
  IssueLabel retries once with a fresh tracking number when the generator
  collides. When the second number is taken too, the request is refused with
  tracking-number-taken and nothing new is stored or published.

  Background:
    Given customer order "ord-1" ships to a synthetic address
    And a label has been issued for package 1 of "PO-1"

  Scenario: A collision that survives the retry is refused
    Given the next two tracking numbers generated are already taken
    When I issue a label for package 1 of "PO-2" for order "ord-1" on tier "NEXT_DAY"
    Then the request is refused with "tracking-number-taken"
    And the published events are "LabelIssued"
