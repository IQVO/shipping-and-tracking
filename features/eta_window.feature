Feature: The day-of ETA window on the journey
  last-mile-delivery states an ETA window per stop when a route starts. The
  journey keeps the latest window next to its scans (it is not a scan) and
  shows it only while the package is out for delivery and the window is not
  older than the last applied scan (ADR 0013).

  Background:
    Given customer order "ord-1" ships to a synthetic address
    And a label has been issued for package 1 of "PO-1"

  Scenario: The window is shown while the package is out for delivery
    Given the package is out for delivery
    When last-mile assigns the ETA window hour 6 to hour 8 at hour 4
    Then the request succeeds
    And the journey is "OUT_FOR_DELIVERY"
    And the last published events are "PackageEtaWindowAssigned"
    And I query the journey of the package
    And the timeline shows the ETA window hour 6 to hour 8

  Scenario: A failed attempt hides the window
    Given the package is out for delivery
    And last-mile assigns the ETA window hour 6 to hour 8 at hour 4
    When I record a "DELIVERY_FAILED" scan "f1" at hour 9
    And I query the journey of the package
    Then the journey is "AT_STATION"
    And the timeline shows no ETA window

  Scenario: Yesterday's window is stale once a newer scan out for delivery exists
    Given the package is out for delivery
    And last-mile assigns the ETA window hour 6 to hour 8 at hour 4
    And I record a "DELIVERY_FAILED" scan "f1" at hour 9
    When I record a "OUT_FOR_DELIVERY" scan "o2" at hour 30
    And I query the journey of the package
    Then the journey is "OUT_FOR_DELIVERY"
    And the timeline shows no ETA window

  Scenario: An older window does not replace a newer one
    Given the package is out for delivery
    And last-mile assigns the ETA window hour 6 to hour 8 at hour 5
    When last-mile assigns the ETA window hour 20 to hour 22 at hour 4
    And I query the journey of the package
    Then the published events include "PackageEtaWindowAssigned" 1 times
    And the timeline shows the ETA window hour 6 to hour 8

  Scenario: A delivered package takes no window
    Given the package is out for delivery
    And I record a "DELIVERED" scan "d1" at hour 6
    When last-mile assigns the ETA window hour 6 to hour 8 at hour 7
    Then the request succeeds
    And the published events include "PackageEtaWindowAssigned" 0 times
    And I query the journey of the package
    And the timeline shows no ETA window
