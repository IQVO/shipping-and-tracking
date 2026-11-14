@rules
Feature: Exception on silence rules from the documentation
  A package that is not scanned within the window of its tier and state raises
  ONE NO_SCAN exception per silence; only an in-transit package whose
  exception stays unanswered for 48 hours is declared lost (docs: use-cases
  RaiseException, aggregate design canvas ExceptionOnSilence, ADR 0002
  A7/A8, troubleshooting "No PackageExceptionRaised for a journey that has
  gone quiet").

  Background:
    Given customer order "ord-1" ships to a synthetic address

  Scenario Outline: Each tier and state has its own silence window
    Given a "<tier>" package has reached "<state>"
    When the clock moves to hour <hour>
    And the silence sweep runs
    Then the sweep raised <raised> exceptions and declared 0 packages lost

    Examples:
      | tier     | state            | hour | raised |
      | NEXT_DAY | HANDED_OVER      | 24   | 0      |
      | NEXT_DAY | HANDED_OVER      | 25   | 1      |
      | TWO_DAY  | AT_STATION       | 50   | 0      |
      | TWO_DAY  | AT_STATION       | 51   | 1      |
      | STANDARD | IN_TRANSIT       | 73   | 0      |
      | STANDARD | IN_TRANSIT       | 74   | 1      |
      | NEXT_DAY | OUT_FOR_DELIVERY | 15   | 0      |
      | NEXT_DAY | OUT_FOR_DELIVERY | 16   | 1      |

  Scenario Outline: A same-day package is silent after six hours
    Given a "SAME_DAY" package has reached "IN_TRANSIT"
    When the clock moves to hour <hour> plus <minutes> minutes
    And the silence sweep runs
    Then the sweep raised <raised> exceptions and declared 0 packages lost

    Examples:
      | hour | minutes | raised |
      | 7    | 59      | 0      |
      | 8    | 1       | 1      |

  Scenario: A package that was never scanned is not watched
    Given a "SAME_DAY" package has reached "LABELLED"
    When the clock moves to hour 500
    And the silence sweep runs
    Then the sweep raised 0 exceptions and declared 0 packages lost
    And the journey is "LABELLED"

  Scenario: A vendor-confirmed package that is still LABELLED is not watched either
    Given a "SAME_DAY" package has reached "LABELLED"
    And I record a "SHIPMENT_CONFIRMED" scan "v1" at hour 1
    When the clock moves to hour 500
    And the silence sweep runs
    Then the sweep raised 0 exceptions and declared 0 packages lost
    And the journey is "LABELLED"

  Scenario: The exception names the package, its order and why
    Given a "NEXT_DAY" package has reached "IN_TRANSIT"
    When the clock moves to hour 26
    And the silence sweep runs
    Then the PackageExceptionRaised event carries reason "NO_SCAN", order "ord-1", assignment "asg-1" and instant hour 26

  Scenario: A package just short of 48 hours after its exception is not yet lost
    Given a "NEXT_DAY" package has reached "IN_TRANSIT"
    And the clock moves to hour 26
    And the silence sweep runs
    When the clock moves to hour 73
    And the silence sweep runs
    Then the sweep raised 0 exceptions and declared 0 packages lost
    And the journey is "IN_TRANSIT"

  Scenario: A lost package is announced with its order and the declaration instant
    Given a "NEXT_DAY" package has reached "IN_TRANSIT"
    And the clock moves to hour 26
    And the silence sweep runs
    When the clock moves to hour 74
    And the silence sweep runs
    Then the PackageLost event carries order "ord-1" and instant hour 74

  Scenario Outline: Only an in-transit package is ever declared lost
    Given a "NEXT_DAY" package has reached "<state>"
    And the clock moves to hour <first>
    And the silence sweep runs
    When the clock moves to hour 200
    And the silence sweep runs
    Then the sweep raised 0 exceptions and declared 0 packages lost
    And the journey is "<state>"

    Examples:
      | state            | first |
      | HANDED_OVER      | 25    |
      | AT_STATION       | 27    |
      | OUT_FOR_DELIVERY | 16    |

  Scenario: A scan answers the exception, so the package is not declared lost
    Given a "NEXT_DAY" package has reached "IN_TRANSIT"
    And the clock moves to hour 26
    And the silence sweep runs
    And I record a "DEPARTURE" scan "s3" at hour 30
    When the clock moves to hour 74
    And the silence sweep runs
    Then the sweep raised 1 exceptions and declared 0 packages lost
    And the journey is "IN_TRANSIT"

  Scenario Outline: The sweep walks every page of silent packages
    Given a label has been issued for package 1 of "PO-1"
    And a label has been issued for package 2 of "PO-1"
    And a label has been issued for package 3 of "PO-1"
    And every package has reached "IN_TRANSIT"
    When the clock moves to hour 100
    And the silence sweep runs reading <pageSize> journeys per page
    Then the sweep raised 3 exceptions and declared 0 packages lost

    Examples:
      | pageSize |
      | 2        |
      | 3        |
