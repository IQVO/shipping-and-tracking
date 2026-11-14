@rules
Feature: Stop events that are not scans, from the documentation
  last-mile-delivery states an ETA window per stop (ADR 0013) and flags a
  delivery point (ADR 0014). Both are applied per tracking number of the stop
  in one unit that claims the CloudEvents id first, republished per package
  with the journey's own order, and never move the journey or answer silence.
  A journey that ended, an unknown tracking number and a stale value are
  counted and acknowledged (docs: aggregate design canvas AssignEta and
  FlagDeliveryPoint, domain-events Consumed, ubiquitous language).

  Background:
    Given customer order "ord-1" ships to a synthetic address
    And customer order "ord-2" ships to the same address under another name
    And a label has been issued for package 1 of "PO-1"

  # --- the day-of ETA window ---------------------------------------------------------

  Scenario: One stop event gives every package of the stop its own window event
    Given I issue a label for package 1 of "PO-2" for order "ord-2" on tier "NEXT_DAY"
    And every package has reached "OUT_FOR_DELIVERY"
    When last-mile assigns the ETA window hour 6 to hour 8 at hour 4 for every package
    Then the relayed event counted 2 applied, 0 stale, 0 closed and 0 unknown
    And the PackageEtaWindowAssigned events carry the orders "ord-1,ord-2", assignment "asg-1" and the window hour 6 to hour 8 assigned at hour 4

  Scenario: An unknown tracking number does not hold back the others
    Given the package has reached "OUT_FOR_DELIVERY"
    When last-mile assigns the ETA window hour 6 to hour 8 at hour 4 for every package and an unknown package
    Then the request succeeds
    And the relayed event counted 1 applied, 0 stale, 0 closed and 1 unknown

  Scenario Outline: A window must end after it starts
    Given the package has reached "OUT_FOR_DELIVERY"
    When last-mile assigns the ETA window hour <from> to hour <to> at hour 4
    Then the request is refused with "validation-failed"
    And the published events include "PackageEtaWindowAssigned" 0 times

    Examples:
      | from | to |
      | 8    | 6  |
      | 6    | 6  |

  Scenario: A window assigned at the same instant as the stored one is stale
    Given the package has reached "OUT_FOR_DELIVERY"
    And last-mile assigns the ETA window hour 6 to hour 8 at hour 5
    When last-mile assigns the ETA window hour 9 to hour 11 at hour 5 for every package
    And I query the journey of the package
    Then the relayed event counted 0 applied, 1 stale, 0 closed and 0 unknown
    And the published events include "PackageEtaWindowAssigned" 1 times
    And the timeline shows the ETA window hour 6 to hour 8

  Scenario: A strictly newer window replaces the stored one
    Given the package has reached "OUT_FOR_DELIVERY"
    And last-mile assigns the ETA window hour 6 to hour 8 at hour 5
    When last-mile assigns the ETA window hour 9 to hour 11 at hour 6
    And I query the journey of the package
    Then the published events include "PackageEtaWindowAssigned" 2 times
    And the timeline shows the ETA window hour 9 to hour 11

  Scenario: A redelivered window event is applied once
    Given the package has reached "OUT_FOR_DELIVERY"
    And last-mile assigns the ETA window hour 6 to hour 8 at hour 5 for every package
    When last-mile assigns the ETA window hour 6 to hour 8 at hour 5 for every package
    Then the relayed event was skipped as already processed
    And the published events include "PackageEtaWindowAssigned" 1 times

  Scenario: A window is not a scan, so it does not answer silence
    Given the package has reached "OUT_FOR_DELIVERY"
    And last-mile assigns the ETA window hour 20 to hour 22 at hour 10
    When the clock moves to hour 16
    And the silence sweep runs
    Then the sweep raised 1 exceptions and declared 0 packages lost
    And the journey has 4 scans of which 4 applied

  Scenario: A window with no start is refused
    Given the package has reached "OUT_FOR_DELIVERY"
    When last-mile assigns an ETA window with no start that ends at hour 8 at hour 4
    Then the request is refused with "validation-failed"
    And the published events include "PackageEtaWindowAssigned" 0 times

  Scenario: A window assigned after a failed attempt is not shown until the package is out again
    Given the package has reached "OUT_FOR_DELIVERY"
    And I record a "DELIVERY_FAILED" scan "f1" at hour 8
    When last-mile assigns the ETA window hour 20 to hour 22 at hour 9
    And I query the journey of the package
    Then the journey is "AT_STATION"
    And the published events include "PackageEtaWindowAssigned" 1 times
    And the timeline shows no ETA window

  Scenario: A delivered package shows no window even when it was assigned at the delivery instant
    Given the package has reached "OUT_FOR_DELIVERY"
    And last-mile assigns the ETA window hour 6 to hour 8 at hour 6
    When I record a "DELIVERED" scan "d1" at hour 6
    And I query the journey of the package
    Then the journey is "DELIVERED"
    And the timeline shows no ETA window

  Scenario: A window that arrives before the out-for-delivery scan is shown once the scan lands
    Given the package has reached "AT_STATION"
    And last-mile assigns the ETA window hour 6 to hour 8 at hour 5
    When I record a "OUT_FOR_DELIVERY" scan "o1" at hour 5
    And I query the journey of the package
    Then the timeline shows the ETA window hour 6 to hour 8

  Scenario Outline: A journey that has ended takes no window
    Given a package that ended as "<end>"
    When last-mile assigns the ETA window hour 60 to hour 62 at hour 59 for every package
    Then the relayed event counted 0 applied, 0 stale, 1 closed and 0 unknown
    And the published events include "PackageEtaWindowAssigned" 0 times

    Examples:
      | end      |
      | RETURNED |
      | VOIDED   |
      | LOST     |

  # --- the delivery-point flag ----------------------------------------------------------

  Scenario: A flag is stored on every journey of the stop and republished with each journey's own order
    Given I issue a label for package 1 of "PO-2" for order "ord-2" on tier "NEXT_DAY"
    When last-mile flags the delivery point "NOT_HOME" at hour 9 for every package
    Then the relayed event counted 2 applied, 0 stale, 0 closed and 0 unknown
    And the delivery point of the first package is flagged "NOT_HOME" at hour 9
    And the delivery point of the second package is flagged "NOT_HOME" at hour 9
    And the PackageDeliveryPointFlagged events carry the reasons "NOT_HOME,NOT_HOME" for the orders "ord-1,ord-2" and assignment "asg-1"
    And both journeys are "LABELLED"
    And the journey has 0 scans of which 0 applied

  Scenario Outline: Both documented reasons are accepted
    When last-mile flags the delivery point "<reason>" at hour 9 for every package
    Then the request succeeds
    And the delivery point of the first package is flagged "<reason>" at hour 9

    Examples:
      | reason         |
      | NOT_HOME       |
      | ACCESS_PROBLEM |

  Scenario: A reason outside the two codes is refused and stores nothing
    When last-mile flags the delivery point "DOG_ON_PREMISES" at hour 9 for every package
    Then the request is refused with "validation-failed"
    And the first package has no delivery-point flag
    And the published events include "PackageDeliveryPointFlagged" 0 times

  Scenario Outline: A flag that is not strictly newer is stale
    Given last-mile flags the delivery point "NOT_HOME" at hour 5 for every package
    When last-mile flags the delivery point "ACCESS_PROBLEM" at hour <hour> for every package
    Then the relayed event counted 0 applied, 1 stale, 0 closed and 0 unknown
    And the delivery point of the first package is flagged "NOT_HOME" at hour 5
    And the published events include "PackageDeliveryPointFlagged" 1 times

    Examples:
      | hour |
      | 5    |
      | 4    |

  Scenario: A newer flag replaces the reason and is announced again
    Given last-mile flags the delivery point "NOT_HOME" at hour 5 for every package
    When last-mile flags the delivery point "ACCESS_PROBLEM" at hour 6 for every package
    Then the relayed event counted 1 applied, 0 stale, 0 closed and 0 unknown
    And the delivery point of the first package is flagged "ACCESS_PROBLEM" at hour 6
    And the PackageDeliveryPointFlagged events carry the reasons "NOT_HOME,ACCESS_PROBLEM" for the orders "ord-1,ord-1" and assignment "asg-1"

  Scenario: A redelivered flag event is applied once
    Given last-mile flags the delivery point "NOT_HOME" at hour 5 for every package
    When last-mile flags the delivery point "NOT_HOME" at hour 5 for every package
    Then the relayed event was skipped as already processed
    And the published events include "PackageDeliveryPointFlagged" 1 times

  Scenario: An unknown tracking number is acknowledged and the others are flagged
    When last-mile flags the delivery point "NOT_HOME" at hour 5 for every package and an unknown package
    Then the request succeeds
    And the relayed event counted 1 applied, 0 stale, 0 closed and 1 unknown
    And the delivery point of the first package is flagged "NOT_HOME" at hour 5

  Scenario Outline: A journey that has ended takes no flag
    Given a package that ended as "<end>"
    When last-mile flags the delivery point "NOT_HOME" at hour 60 for every package
    Then the request succeeds
    And the relayed event counted 0 applied, 0 stale, 1 closed and 0 unknown
    And the first package has no delivery-point flag
    And the published events include "PackageDeliveryPointFlagged" 0 times

    Examples:
      | end       |
      | DELIVERED |
      | RETURNED  |
      | VOIDED    |
      | LOST      |

  Scenario: A flag does not move the journey and does not answer silence
    Given the package has reached "OUT_FOR_DELIVERY"
    And last-mile flags the delivery point "ACCESS_PROBLEM" at hour 10 for every package
    When the clock moves to hour 16
    And the silence sweep runs
    Then the sweep raised 1 exceptions and declared 0 packages lost
    And the journey is "OUT_FOR_DELIVERY"
    And the journey has 4 scans of which 4 applied
