Feature: Issue a shipping label
  A label is issued once per package of a purchase order. The ship-to address
  is read from customer-ordering at issue time, stored only as an encrypted
  snapshot and never published; the journey of the package starts LABELLED.

  Background:
    Given customer order "ord-1" ships to a synthetic address
    And customer order "ord-2" ships to the same address under another name

  Scenario: Issuing a label starts the journey and publishes LabelIssued
    When I issue a label for package 1 of "PO-1" for order "ord-1" on tier "NEXT_DAY"
    Then the request succeeds
    And the label has carrier "RN-EXPRESS" and status "ISSUED"
    And the journey is "LABELLED"
    And the published events are "LabelIssued"
    And a ship-to snapshot is stored

  Scenario: A slower tier rides the ground carrier
    When I issue a label for package 1 of "PO-1" for order "ord-1" on tier "STANDARD"
    Then the label has carrier "RN-GROUND" and status "ISSUED"

  Scenario: One label per package of a purchase order
    Given a label has been issued for package 1 of "PO-1"
    When I issue a label for package 1 of "PO-1" for order "ord-1" on tier "NEXT_DAY"
    Then the request is refused with "label-already-issued"
    And the published events are "LabelIssued"

  Scenario: Another package of the same purchase order gets its own label
    Given a label has been issued for package 1 of "PO-1"
    When I issue a label for package 2 of "PO-1" for order "ord-1" on tier "NEXT_DAY"
    Then the request succeeds
    And the published events are "LabelIssued,LabelIssued"

  Scenario: Neighbours at one address share a stop key
    Given a label has been issued for package 1 of "PO-1"
    When I issue a label for package 1 of "PO-2" for order "ord-2" on tier "NEXT_DAY"
    Then the request succeeds
    And the two LabelIssued events carry the same stop key and postal prefix "01310"

  Scenario: No published event carries the address
    When I issue a label for package 1 of "PO-1" for order "ord-1" on tier "NEXT_DAY"
    Then no published event or response contains any part of the address

  Scenario: An unknown order cannot be labelled
    When I issue a label for package 1 of "PO-9" for order "ord-unknown" on tier "NEXT_DAY"
    Then the request is refused with "validation-failed"
    And nothing is stored

  Scenario: Nothing is stored when customer-ordering is down
    Given customer-ordering is unavailable
    When I issue a label for package 1 of "PO-1" for order "ord-1" on tier "NEXT_DAY"
    Then the request is refused with "ship-to-unavailable"
    And nothing is stored

  Scenario: A tracking number collision is retried once with a new number
    Given a label has been issued for package 1 of "PO-1"
    And the next tracking number generated is the one already taken
    When I issue a label for package 1 of "PO-2" for order "ord-2" on tier "NEXT_DAY"
    Then the request succeeds
    And the two labels have different tracking numbers
