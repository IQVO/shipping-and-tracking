@rules
Feature: Ship-to retention and the catalog view, from the documentation
  The encrypted ship-to is purged 30 days after its journey closed, whichever
  end state it reached, and never while the package is still open. The
  CatalogDimensionsView is an event-fed upsert on catalogItemId and is
  naturally idempotent (docs: aggregate design canvas ShippingLabel and read
  models, domain-events Idempotency, ADR 0002 A9/A18).

  Background:
    Given customer order "ord-1" ships to a synthetic address
    And a label has been issued for package 1 of "PO-1"

  Scenario Outline: The snapshot of every closed journey is purged after the retention
    Given a package that ended as "<end>"
    When the clock moves forward 30 days and a second after the journey closed
    And the ship-to purge runs
    Then no ship-to snapshot is stored

    Examples:
      | end       |
      | DELIVERED |
      | RETURNED  |
      | LOST      |

  Scenario: A delivered package keeps its snapshot until the retention has passed
    Given a package that ended as "DELIVERED"
    When the clock moves forward 30 days after the journey closed
    And the ship-to purge runs
    Then a ship-to snapshot is stored

  Scenario: An open journey keeps its snapshot however old
    Given the package has reached "IN_TRANSIT"
    When the clock moves to hour 2160
    And the ship-to purge runs
    Then a ship-to snapshot is stored
    And the journey is "IN_TRANSIT"

  Scenario: Purging a closed journey leaves the snapshot of a package that is still open
    Given a second label has been issued for package 2 of "PO-1"
    And a package that ended as "VOIDED"
    When the clock moves forward 30 days and a second after the journey closed
    And the ship-to purge runs
    Then a ship-to snapshot is stored

  Scenario: A catalog item is upserted by its id
    Given catalog item "cat-mug-01" weighing 350 g is recorded by event "ev-cat-1"
    When catalog item "cat-mug-01" weighing 420 g is recorded by event "ev-cat-2"
    Then the catalog view holds 1 item and item "cat-mug-01" weighs 420 g

  Scenario: Another catalog item gets its own row
    Given catalog item "cat-mug-01" weighing 350 g is recorded by event "ev-cat-1"
    When catalog item "cat-lamp-07" weighing 1200 g is recorded by event "ev-cat-2"
    Then the catalog view holds 2 items and item "cat-lamp-07" weighs 1200 g

  Scenario: A redelivered catalog event changes nothing
    Given catalog item "cat-mug-01" weighing 350 g is recorded by event "ev-cat-1"
    When catalog item "cat-mug-01" weighing 999 g is recorded by event "ev-cat-1"
    Then the catalog event was skipped as already processed
    And the catalog view holds 1 item and item "cat-mug-01" weighs 350 g

  Scenario: A catalog item with an invalid id is refused
    When catalog item "bad id!" weighing 350 g is recorded by event "ev-cat-9"
    Then the request is refused with "validation-failed"
    And the catalog view is empty
