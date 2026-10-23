Feature: Exception on silence
  A package that is not scanned within the window of its tier raises one
  NO_SCAN exception per silence. An in-transit package whose exception stays
  unanswered for 48 hours is declared lost.

  Background:
    Given customer order "ord-1" ships to a synthetic address
    And a label has been issued for package 1 of "PO-1"
    And I record a "PICKUP_DEPARTURE" scan "s1" at hour 1
    And I record a "DEPARTURE" scan "s2" at hour 2

  Scenario: Inside the window nothing happens
    When the clock moves to hour 25
    And the silence sweep runs
    Then the sweep raised 0 exceptions and declared 0 packages lost

  Scenario: At the end of the window one exception is raised
    When the clock moves to hour 26
    And the silence sweep runs
    Then the sweep raised 1 exceptions and declared 0 packages lost
    And the last published events are "PackageExceptionRaised"
    And the journey is "IN_TRANSIT"

  Scenario: The same silence raises only one exception
    Given the clock moves to hour 26
    And the silence sweep runs
    When the clock moves to hour 40
    And the silence sweep runs
    Then the sweep raised 0 exceptions and declared 0 packages lost

  Scenario: A scan after the exception ends the silence
    Given the clock moves to hour 26
    And the silence sweep runs
    And I record a "DEPARTURE" scan "s3" at hour 30
    When the clock moves to hour 60
    And the silence sweep runs
    Then the sweep raised 1 exceptions and declared 0 packages lost

  Scenario: An unanswered exception becomes a lost package after 48 hours
    Given the clock moves to hour 26
    And the silence sweep runs
    When the clock moves to hour 74
    And the silence sweep runs
    Then the sweep raised 0 exceptions and declared 1 packages lost
    And the journey is "LOST"
    And the last published events are "PackageLost"
