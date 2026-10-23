Feature: The ship-to snapshot is purged 30 days after the journey closes
  The encrypted ship-to is kept only as long as the package may still be
  delivered, returned or disputed.

  Background:
    Given customer order "ord-1" ships to a synthetic address
    And a label has been issued for package 1 of "PO-1"
    And I void the label with reason "order cancelled"

  Scenario: Kept until the retention has passed
    When the clock moves forward 30 days after the journey closed
    And the ship-to purge runs
    Then a ship-to snapshot is stored

  Scenario: Purged after the retention
    When the clock moves forward 30 days and a second after the journey closed
    And the ship-to purge runs
    Then no ship-to snapshot is stored
    And the journey is "VOIDED"
